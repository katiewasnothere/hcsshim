//go:build windows

package uvm

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/Microsoft/hcsshim/internal/copyfile"
	"github.com/Microsoft/hcsshim/internal/hcs/resourcepaths"
	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/protocol/guestrequest"
	"github.com/Microsoft/hcsshim/internal/protocol/guestresource"
	"github.com/Microsoft/hcsshim/internal/security"
	"github.com/Microsoft/hcsshim/internal/wclayer"
)

// VMAccessType is used to determine the various types of access we can
// grant for a given file.
type VMAccessType int

const (
	// `VMAccessTypeNoop` indicates no additional access should be given. Note
	// this should be used for layers and gpu vhd where we have given VM group
	// access outside of the shim (containerd for layers, package installation
	// for gpu vhd).
	VMAccessTypeNoop VMAccessType = iota
	// `VMAccessTypeGroup` indicates we should give access to a file for the VM group sid
	VMAccessTypeGroup
	// `VMAccessTypeIndividual` indicates we should give additional access to a file for
	// the running VM only
	VMAccessTypeIndividual
)

const scsiCurrentSerialVersionID = 2

var (
	ErrNoAvailableLocation            = fmt.Errorf("no available location")
	ErrNotAttached                    = fmt.Errorf("not attached")
	ErrAlreadyAttached                = fmt.Errorf("already attached")
	ErrNoSCSIControllers              = fmt.Errorf("no SCSI controllers configured for this utility VM")
	ErrTooManyAttachments             = fmt.Errorf("too many SCSI attachments")
	ErrSCSILayerWCOWUnsupported       = fmt.Errorf("SCSI attached layers are not supported for WCOW")
	ErrSCSIPartitionedWCWOUnsupported = fmt.Errorf("SCSI partitioned layers are not supported for WCOW")
	ErrGuestMountNotFound             = fmt.Errorf("SCSI guest mount not found")
)

// Release frees the resources of the corresponding Scsi Mount
func (sm *SCSISingleGuestMount) Release(ctx context.Context) error {
	if err := sm.vm.RemoveSCSI(ctx, sm.scsiAttachment.HostPath); err != nil {
		return fmt.Errorf("failed to remove SCSI device: %s", err)
	}
	return nil
}

type SCSISingleGuestMount struct {
	uvmPath string
	// used for cleaning up guest mounts
	// scsiAttachment *SCSIAttachment
	vm             *UtilityVM
	scsiAttachment *SCSIAttachment
}

// addSCSIRequest is an internal struct used to hold all the parameters that are sent to
// the addSCSIActual method.
type addSCSIRequest struct {
	// host path to the disk that should be added as a SCSI disk.
	hostPath string
	// the path inside the uvm at which this disk should show up. Can be empty.
	uvmPath string
	// optional partition to use, 0 if not a partitioned disk
	partition uint8
	// attachmentType is required and `must` be `VirtualDisk` for vhd/vhdx
	// attachments, `PassThru` for physical disk and `ExtensibleVirtualDisk` for
	// Extensible virtual disks.
	attachmentType string
	// indicates if the VHD is encrypted
	encrypted bool
	// indicates if the attachment should be added read only.
	readOnly bool
	// guestOptions is a slice that contains optional information to pass to the guest
	// service.
	guestOptions []string
	// indicates what access to grant the vm for the hostpath. Only required for
	// `VirtualDisk` and `PassThru` disk types.
	vmAccess VMAccessType
	// `evdType` indicates the type of the extensible virtual disk if `attachmentType`
	// is "ExtensibleVirtualDisk" should be empty otherwise.
	evdType string
}

// RefCount returns the current refcount for the SCSI mount.
func (sm *SCSISingleGuestMount) RefCount() uint32 {
	return sm.scsiAttachment.refCount
}

func (sm *SCSISingleGuestMount) UVMPath() string {
	return sm.uvmPath
}

func (gm *SCSISingleGuestMount) logFormat() logrus.Fields {
	return logrus.Fields{
		"UVMPath":  gm.uvmPath,
		"HostPath": gm.scsiAttachment.HostPath,
	}
}

// allocateSCSISlot finds the next available slot on the
// SCSI controllers associated with a utility VM to use.
// Lock must be held when calling this function
func (uvm *UtilityVM) allocateSCSISlot(ctx context.Context) (int, int, error) {
	for controller := 0; controller < int(uvm.scsiControllerCount); controller++ {
		for lun, sm := range uvm.scsiLocations[controller] {
			// If sm is nil, we have found an open slot so we allocate a new SCSIMount
			if sm == nil {
				return controller, lun, nil
			}
		}
	}
	return -1, -1, ErrNoAvailableLocation
}

func (uvm *UtilityVM) deallocateSCSISlot(ctx context.Context, sm *SCSIAttachment) {
	uvm.m.Lock()
	defer uvm.m.Unlock()
	if sm != nil {
		log.G(ctx).WithFields(sm.logFormat()).Debug("removed SCSI location")
		uvm.scsiLocations[sm.Controller][sm.LUN] = nil
	}
}

func (uvm *UtilityVM) deallocateSCSIGuestMount(ctx context.Context, gm *SCSISingleGuestMount) {
	uvm.m.Lock()
	defer uvm.m.Unlock()
	if gm != nil {
		log.G(ctx).WithFields(gm.logFormat()).Debug("removed SCSI guest location")
		// remove gm from sm
		uvm.scsiMounts[gm.scsiAttachment.HostPath] = nil
	}
}

// Lock must be held when calling this function.
func (uvm *UtilityVM) findSCSIAttachment(ctx context.Context, findThisHostPath string) (*SCSIAttachment, error) {
	for _, luns := range uvm.scsiLocations {
		for _, sm := range luns {
			if sm != nil && sm.HostPath == findThisHostPath {
				log.G(ctx).WithFields(sm.logFormat()).Debug("found SCSI location")
				return sm, nil
			}
		}
	}
	return nil, ErrNotAttached
}

func (uvm *UtilityVM) findSCSIGuestMount(ctx context.Context, findThisHostPath string) (*SCSISingleGuestMount, error) {
	for _, gm := range uvm.scsiMounts {
		if gm.scsiAttachment.HostPath == findThisHostPath {
			log.G(ctx).WithFields(gm.logFormat()).Debug("found SCSI Guest Mount")
			return gm, nil
		}
	}
	return nil, ErrGuestMountNotFound
}

// RemoveSCSI removes a SCSI disk from a utility VM.
func (uvm *UtilityVM) RemoveSCSI(ctx context.Context, hostPath string) error {
	uvm.m.Lock()
	defer uvm.m.Unlock()

	if uvm.scsiControllerCount == 0 {
		return ErrNoSCSIControllers
	}

	// Make sure it is actually attached
	sm, err := uvm.findSCSIAttachment(ctx, hostPath)
	if err != nil {
		return err
	}

	gm, err := uvm.findSCSIGuestMount(ctx, hostPath)
	if err != nil {
		return err
	}

	sm.refCount--
	if sm.refCount > 0 {
		return nil
	}

	scsiModification := &hcsschema.ModifySettingRequest{
		RequestType:  guestrequest.RequestTypeRemove,
		ResourcePath: fmt.Sprintf(resourcepaths.SCSIResourceFormat, guestrequest.ScsiControllerGuids[sm.Controller], sm.LUN),
	}

	var verity *guestresource.DeviceVerityInfo
	if v, iErr := readVeritySuperBlock(ctx, hostPath); iErr != nil {
		log.G(ctx).WithError(iErr).WithField("hostPath", sm.HostPath).Debug("unable to read dm-verity information from VHD")
	} else {
		if v != nil {
			log.G(ctx).WithFields(logrus.Fields{
				"hostPath":   hostPath,
				"rootDigest": v.RootDigest,
			}).Debug("removing SCSI with dm-verity")
		}
		verity = v
	}

	// Include the GuestRequest so that the GCS ejects the disk cleanly if the
	// disk was attached/mounted
	//
	// Note: We always send a guest eject even if there is no UVM path in lcow
	// so that we synchronize the guest state. This seems to always avoid SCSI
	// related errors if this index quickly reused by another container.
	if uvm.operatingSystem == "windows" && gm.uvmPath != "" {
		scsiModification.GuestRequest = guestrequest.ModificationRequest{
			ResourceType: guestresource.ResourceTypeMappedVirtualDisk,
			RequestType:  guestrequest.RequestTypeRemove,
			Settings: guestresource.WCOWMappedVirtualDisk{
				ContainerPath: gm.uvmPath,
				Lun:           sm.LUN,
			},
		}
	} else {
		scsiModification.GuestRequest = guestrequest.ModificationRequest{
			ResourceType: guestresource.ResourceTypeMappedVirtualDisk,
			RequestType:  guestrequest.RequestTypeRemove,
			Settings: guestresource.LCOWMappedVirtualDisk{
				MountPath:  gm.uvmPath, // May be blank in attach-only
				Lun:        uint8(sm.LUN),
				Controller: uint8(sm.Controller),
				VerityInfo: verity,
			},
		}
	}

	if err := uvm.modify(ctx, scsiModification); err != nil {
		return fmt.Errorf("failed to remove SCSI disk %s from container %s: %s", hostPath, uvm.id, err)
	}
	log.G(ctx).WithFields(sm.logFormat()).Debug("removed SCSI location")
	uvm.scsiLocations[sm.Controller][sm.LUN] = nil
	return nil
}

// AddSCSI adds a SCSI disk to a utility VM at the next available location. This
// function should be called for adding a scratch layer, a read-only layer as an
// alternative to VPMEM, or for other VHD mounts.
//
// `hostPath` is required and must point to a vhd/vhdx path.
//
// `uvmPath` is optional. If not provided, no guest request will be made
//
// `readOnly` set to `true` if the vhd/vhdx should be attached read only.
//
// `encrypted` set to `true` if the vhd/vhdx should be attached in encrypted mode.
// The device will be formatted, so this option must be used only when creating
// scratch vhd/vhdx.
//
// `guestOptions` is a slice that contains optional information to pass
// to the guest service
//
// `vmAccess` indicates what access to grant the vm for the hostpath
func (uvm *UtilityVM) AddSCSI(
	ctx context.Context,
	hostPath string,
	uvmPath string,
	readOnly bool,
	encrypted bool,
	guestOptions []string,
	vmAccess VMAccessType,
) (*SCSISingleGuestMount, error) {
	addReq := &addSCSIRequest{
		hostPath:       hostPath,
		uvmPath:        uvmPath,
		attachmentType: "VirtualDisk",
		readOnly:       readOnly,
		encrypted:      encrypted,
		guestOptions:   guestOptions,
		vmAccess:       vmAccess,
	}
	return uvm.addSCSIActual(ctx, addReq)
}

// AddSCSIPhysicalDisk attaches a physical disk from the host directly to the
// Utility VM at the next available location.
//
// `hostPath` is required and `likely` start's with `\\.\PHYSICALDRIVE`.
//
// `uvmPath` is optional if a guest mount is not requested.
//
// `readOnly` set to `true` if the physical disk should be attached read only.
//
// `guestOptions` is a slice that contains optional information to pass
// to the guest service
func (uvm *UtilityVM) AddSCSIPhysicalDisk(ctx context.Context, hostPath, uvmPath string, readOnly bool, guestOptions []string) (*SCSISingleGuestMount, error) {
	addReq := &addSCSIRequest{
		hostPath:       hostPath,
		uvmPath:        uvmPath,
		attachmentType: "PassThru",
		readOnly:       readOnly,
		guestOptions:   guestOptions,
		vmAccess:       VMAccessTypeIndividual,
	}
	return uvm.addSCSIActual(ctx, addReq)
}

// AddSCSIExtensibleVirtualDisk adds an extensible virtual disk as a SCSI mount
// to the utility VM at the next available location. All such disks which are not actual virtual disks
// but provide the same SCSI interface are added to the UVM as Extensible Virtual disks.
//
// `hostPath` is required. Depending on the type of the extensible virtual disk the format of `hostPath` can
// be different.
// For example, in case of storage spaces the host path must be in the
// `evd://space/{storage_pool_unique_ID}{virtual_disk_unique_ID}` format.
//
// `uvmPath` must be provided in order to be able to use this disk in a container.
//
// `readOnly` set to `true` if the virtual disk should be attached read only.
//
// `vmAccess` indicates what access to grant the vm for the hostpath
func (uvm *UtilityVM) AddSCSIExtensibleVirtualDisk(ctx context.Context, hostPath, uvmPath string, readOnly bool) (*SCSISingleGuestMount, error) {
	if uvmPath == "" {
		return nil, errors.New("uvmPath can not be empty for extensible virtual disk")
	}
	evdType, mountPath, err := ParseExtensibleVirtualDiskPath(hostPath)
	if err != nil {
		return nil, err
	}
	addReq := &addSCSIRequest{
		hostPath:       mountPath,
		uvmPath:        uvmPath,
		attachmentType: "ExtensibleVirtualDisk",
		readOnly:       readOnly,
		guestOptions:   []string{},
		vmAccess:       VMAccessTypeIndividual,
		evdType:        evdType,
	}
	return uvm.addSCSIActual(ctx, addReq)
}

// addSCSIActual is the implementation behind the external functions AddSCSI,
// AddSCSIPhysicalDisk, AddSCSIExtensibleVirtualDisk.
//
// We are in control of everything ourselves. Hence we have ref- counting and
// so-on tracking what SCSI locations are available or used.
//
// Returns result from calling modify with the given scsi mount
func (uvm *UtilityVM) addSCSIActual(ctx context.Context, addReq *addSCSIRequest) (_ *SCSISingleGuestMount, err error) {
	sm, _, err := uvm.allocateSCSIAttachment(
		ctx,
		addReq.readOnly,
		addReq.encrypted,
		addReq.hostPath,
		addReq.attachmentType,
		addReq.evdType,
		addReq.vmAccess,
	)
	if err != nil {
		return nil, err
	}

	gm, guestMountExisted, err := uvm.allocateSCSIGuestMount(ctx, sm, addReq.uvmPath)
	if err != nil {
		return nil, err
	}

	if guestMountExisted {
		// another mount request might be in progress, wait for it to finish and if that operation
		// fails return that error.
		<-sm.waitCh
		if sm.waitErr != nil {
			return nil, sm.waitErr
		}
		return gm, nil
	}

	// This is the first goroutine to add this disk, close the waitCh after we are done.
	defer func() {
		if err != nil {
			uvm.deallocateSCSISlot(ctx, sm)
			uvm.deallocateSCSIGuestMount(ctx, gm)
		}

		// error must be set _before_ the channel is closed.
		sm.waitErr = err
		close(sm.waitCh)
	}()

	SCSIModification := &hcsschema.ModifySettingRequest{
		RequestType: guestrequest.RequestTypeAdd,
		Settings: hcsschema.Attachment{
			Path:                      sm.HostPath,
			Type_:                     addReq.attachmentType,
			ReadOnly:                  addReq.readOnly,
			ExtensibleVirtualDiskType: addReq.evdType,
		},
		ResourcePath: fmt.Sprintf(resourcepaths.SCSIResourceFormat, guestrequest.ScsiControllerGuids[sm.Controller], sm.LUN),
	}

	if gm.uvmPath != "" {
		guestReq := guestrequest.ModificationRequest{
			ResourceType: guestresource.ResourceTypeMappedVirtualDisk,
			RequestType:  guestrequest.RequestTypeAdd,
		}

		if uvm.operatingSystem == "windows" {
			guestReq.Settings = guestresource.WCOWMappedVirtualDisk{
				ContainerPath: gm.uvmPath,
				Lun:           sm.LUN,
			}
		} else {
			var verity *guestresource.DeviceVerityInfo
			if v, iErr := readVeritySuperBlock(ctx, sm.HostPath); iErr != nil {
				log.G(ctx).WithError(iErr).WithField("hostPath", sm.HostPath).Debug("unable to read dm-verity information from VHD")
			} else {
				if v != nil {
					log.G(ctx).WithFields(logrus.Fields{
						"hostPath":   sm.HostPath,
						"rootDigest": v.RootDigest,
					}).Debug("adding SCSI with dm-verity")
				}
				verity = v
			}

			guestReq.Settings = guestresource.LCOWMappedVirtualDisk{
				MountPath:  gm.uvmPath,
				Lun:        uint8(sm.LUN),
				Controller: uint8(sm.Controller),
				ReadOnly:   addReq.readOnly,
				Encrypted:  addReq.encrypted,
				Options:    addReq.guestOptions,
				VerityInfo: verity,
			}
		}
		SCSIModification.GuestRequest = guestReq
	}

	if err := uvm.modify(ctx, SCSIModification); err != nil {
		return nil, fmt.Errorf("failed to modify UVM with new SCSI mount: %s", err)
	}
	return gm, nil
}

// allocateSCSIMount grants vm access to hostpath and increments the ref count of an existing scsi
// device or allocates a new one if not already present.
// Returns the resulting *SCSIMount, a bool indicating if the scsi device was already present,
// and error if any.
func (uvm *UtilityVM) allocateSCSIAttachment(
	ctx context.Context,
	readOnly bool,
	encrypted bool,
	hostPath string,
	attachmentType string,
	evdType string,
	vmAccess VMAccessType,
) (*SCSIAttachment, bool, error) {
	if attachmentType != "ExtensibleVirtualDisk" {
		// Ensure the utility VM has access
		err := grantAccess(ctx, uvm.id, hostPath, vmAccess)
		if err != nil {
			return nil, false, errors.Wrapf(err, "failed to grant VM access for SCSI mount")
		}
	}
	// We must hold the lock throughout the lookup (findSCSIAttachment) until
	// after the possible allocation (allocateSCSISlot) has been completed to ensure
	// there isn't a race condition for it being attached by another thread between
	// these two operations.
	uvm.m.Lock()
	defer uvm.m.Unlock()
	if sm, err := uvm.findSCSIAttachment(ctx, hostPath); err == nil {
		sm.refCount++
		return sm, true, nil
	}

	controller, lun, err := uvm.allocateSCSISlot(ctx)
	if err != nil {
		return nil, false, err
	}

	uvm.scsiLocations[controller][lun] = newSCSIMount(
		uvm,
		hostPath,
		attachmentType,
		evdType,
		1,
		controller,
		int32(lun),
		readOnly,
		encrypted,
	)

	log.G(ctx).WithFields(uvm.scsiLocations[controller][lun].logFormat()).Debug("allocated SCSI mount")

	return uvm.scsiLocations[controller][lun], false, nil
}

func (uvm *UtilityVM) allocateSCSIGuestMount(
	ctx context.Context,
	scsiAttachment *SCSIAttachment,
	uvmPath string,
) (*SCSISingleGuestMount, bool, error) {
	// We must hold the lock throughout the lookup (findSCSIAttachment) until
	// after the possible allocation (allocateSCSISlot) has been completed to ensure
	// there isn't a race condition for it being attached by another thread between
	// these two operations.

	// TODO katiewasnothere: probably need this too right?
	uvm.m.Lock()
	defer uvm.m.Unlock()

	if gm, err := uvm.findSCSIGuestMount(ctx, scsiAttachment.HostPath); err == nil {
		// guest mount already exists, just increment the ref count
		gm.scsiAttachment.refCount++
		return gm, true, nil
	}

	// TODO katiewasnothere: check that partition matches too

	// guest mount doesn't exist, create a new one
	gm := newSCSISingleGuestMount(uvm, scsiAttachment, uvmPath)
	uvm.scsiMounts[scsiAttachment.HostPath] = gm
	return gm, false, nil
}

func newSCSISingleGuestMount(
	vm *UtilityVM,
	scsiAttachment *SCSIAttachment,
	uvmPath string,
) *SCSISingleGuestMount {
	return &SCSISingleGuestMount{
		vm: vm,
		// hostPath:  hostPath,
		scsiAttachment: scsiAttachment,
		uvmPath:        uvmPath,
	}
}

// GetScsiUvmPath returns the guest mounted path of a SCSI drive.
//
// If `hostPath` is not mounted returns `ErrNotAttached`.
func (uvm *UtilityVM) GetSCSISingleUVMPath(ctx context.Context, hostPath string) (string, error) {
	uvm.m.Lock()
	defer uvm.m.Unlock()
	gm, err := uvm.findSCSIGuestMount(ctx, hostPath)
	if err != nil {
		return "", nil
	}
	return gm.uvmPath, err
}

func (uvm *UtilityVM) GetSCSIGuestMount(ctx context.Context, hostPath string) (*SCSISingleGuestMount, error) {
	uvm.m.Lock()
	defer uvm.m.Unlock()
	gm, err := uvm.findSCSIGuestMount(ctx, hostPath)
	if err != nil {
		return nil, err
	}
	return gm, err
}

// ScratchEncryptionEnabled is a getter for `uvm.encryptScratch`.
//
// Returns true if the scratch disks should be encrypted, false otherwise.
func (uvm *UtilityVM) ScratchEncryptionEnabled() bool {
	return uvm.encryptScratch
}

// grantAccess helper function to grant access to a file for the vm or vm group
func grantAccess(ctx context.Context, uvmID string, hostPath string, vmAccess VMAccessType) error {
	switch vmAccess {
	case VMAccessTypeGroup:
		log.G(ctx).WithField("path", hostPath).Debug("granting vm group access")
		return security.GrantVmGroupAccess(hostPath)
	case VMAccessTypeIndividual:
		return wclayer.GrantVmAccess(ctx, uvmID, hostPath)
	}
	return nil
}

var _ = (Cloneable)(&SCSIAttachment{})

// GobEncode serializes the SCSIMount struct
func (sm *SCSIAttachment) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	encoder := gob.NewEncoder(&buf)
	errMsgFmt := "failed to encode SCSIMount: %s"
	// encode only the fields that can be safely deserialized.
	if err := encoder.Encode(sm.serialVersionID); err != nil {
		return nil, fmt.Errorf(errMsgFmt, err)
	}
	if err := encoder.Encode(sm.HostPath); err != nil {
		return nil, fmt.Errorf(errMsgFmt, err)
	}
	if err := encoder.Encode(sm.Controller); err != nil {
		return nil, fmt.Errorf(errMsgFmt, err)
	}
	if err := encoder.Encode(sm.LUN); err != nil {
		return nil, fmt.Errorf(errMsgFmt, err)
	}
	if err := encoder.Encode(sm.readOnly); err != nil {
		return nil, fmt.Errorf(errMsgFmt, err)
	}
	if err := encoder.Encode(sm.attachmentType); err != nil {
		return nil, fmt.Errorf(errMsgFmt, err)
	}
	if err := encoder.Encode(sm.extensibleVirtualDiskType); err != nil {
		return nil, fmt.Errorf(errMsgFmt, err)
	}
	return buf.Bytes(), nil
}

// GobDecode deserializes the SCSIMount struct into the struct on which this is called
// (i.e the sm pointer)
func (sm *SCSIAttachment) GobDecode(data []byte) error {
	buf := bytes.NewBuffer(data)
	decoder := gob.NewDecoder(buf)
	errMsgFmt := "failed to decode SCSIMount: %s"
	// fields should be decoded in the same order in which they were encoded.
	if err := decoder.Decode(&sm.serialVersionID); err != nil {
		return fmt.Errorf(errMsgFmt, err)
	}
	if sm.serialVersionID != scsiCurrentSerialVersionID {
		return fmt.Errorf("serialized version of SCSIMount: %d doesn't match with the current version: %d", sm.serialVersionID, scsiCurrentSerialVersionID)
	}
	if err := decoder.Decode(&sm.HostPath); err != nil {
		return fmt.Errorf(errMsgFmt, err)
	}
	if err := decoder.Decode(&sm.Controller); err != nil {
		return fmt.Errorf(errMsgFmt, err)
	}
	if err := decoder.Decode(&sm.LUN); err != nil {
		return fmt.Errorf(errMsgFmt, err)
	}
	if err := decoder.Decode(&sm.readOnly); err != nil {
		return fmt.Errorf(errMsgFmt, err)
	}
	if err := decoder.Decode(&sm.attachmentType); err != nil {
		return fmt.Errorf(errMsgFmt, err)
	}
	if err := decoder.Decode(&sm.extensibleVirtualDiskType); err != nil {
		return fmt.Errorf(errMsgFmt, err)
	}
	return nil
}

// Clone function creates a clone of the SCSIMount `sm` and adds the cloned SCSIMount to
// the uvm `vm`. If `sm` is read only then it is simply added to the `vm`. But if it is a
// writable mount(e.g a scratch layer) then a copy of it is made and that copy is added
// to the `vm`.
func (sm *SCSIAttachment) Clone(ctx context.Context, vm *UtilityVM, cd *cloneData) error {
	var (
		dstVhdPath string = sm.HostPath
		err        error
		dir        string
		conStr     string = guestrequest.ScsiControllerGuids[sm.Controller]
		lunStr     string = fmt.Sprintf("%d", sm.LUN)
	)

	if !sm.readOnly {
		// This is a writable SCSI mount. It must be either the
		// 1. scratch VHD of the UVM or
		// 2. scratch VHD of the container.
		// A user provided writable SCSI mount is not allowed on the template UVM
		// or container and so this SCSI mount has to be the scratch VHD of the
		// UVM or container.  The container inside this UVM will automatically be
		// cloned here when we are cloning the uvm itself. We will receive a
		// request for creation of this container later and that request will
		// specify the storage path for this container.  However, that storage
		// location is not available now so we just use the storage path of the
		// uvm instead.
		// TODO(ambarve): Find a better way for handling this. Problem with this
		// approach is that the scratch VHD of the container will not be
		// automatically cleaned after container exits. It will stay there as long
		// as the UVM keeps running.

		// For the scratch VHD of the VM (always attached at Controller:0, LUN:0)
		// clone it in the scratch folder
		dir = cd.scratchFolder
		if sm.Controller != 0 || sm.LUN != 0 {
			dir, err = os.MkdirTemp(cd.scratchFolder, fmt.Sprintf("clone-mount-%d-%d", sm.Controller, sm.LUN))
			if err != nil {
				return fmt.Errorf("error while creating directory for scsi mounts of clone vm: %s", err)
			}
		}

		// copy the VHDX
		dstVhdPath = filepath.Join(dir, filepath.Base(sm.HostPath))
		log.G(ctx).WithFields(logrus.Fields{
			"source hostPath":      sm.HostPath,
			"controller":           sm.Controller,
			"LUN":                  sm.LUN,
			"destination hostPath": dstVhdPath,
		}).Debug("Creating a clone of SCSI mount")

		if err = copyfile.CopyFile(ctx, sm.HostPath, dstVhdPath, true); err != nil {
			return err
		}

		if err = grantAccess(ctx, cd.uvmID, dstVhdPath, VMAccessTypeIndividual); err != nil {
			os.Remove(dstVhdPath)
			return err
		}
	}

	if cd.doc.VirtualMachine.Devices.Scsi == nil {
		cd.doc.VirtualMachine.Devices.Scsi = map[string]hcsschema.Scsi{}
	}

	if _, ok := cd.doc.VirtualMachine.Devices.Scsi[conStr]; !ok {
		cd.doc.VirtualMachine.Devices.Scsi[conStr] = hcsschema.Scsi{
			Attachments: map[string]hcsschema.Attachment{},
		}
	}

	cd.doc.VirtualMachine.Devices.Scsi[conStr].Attachments[lunStr] = hcsschema.Attachment{
		Path:  dstVhdPath,
		Type_: sm.attachmentType,
	}

	clonedScsiMount := newSCSIMount(
		vm,
		dstVhdPath,
		sm.attachmentType,
		sm.extensibleVirtualDiskType,
		1,
		sm.Controller,
		sm.LUN,
		sm.readOnly,
		sm.encrypted,
	)

	vm.scsiLocations[sm.Controller][sm.LUN] = clonedScsiMount

	// TODO add the scsi guest mounts to the cloned scsi mount

	return nil
}

// ParseExtensibleVirtualDiskPath parses the evd path provided in the config.
// extensible virtual disk path has format "evd://<evdType>/<evd-mount-path>"
// this function parses that and returns the `evdType` and `evd-mount-path`.
func ParseExtensibleVirtualDiskPath(hostPath string) (evdType, mountPath string, err error) {
	trimmedPath := strings.TrimPrefix(hostPath, "evd://")
	separatorIndex := strings.Index(trimmedPath, "/")
	if separatorIndex <= 0 {
		return "", "", errors.Errorf("invalid extensible vhd path: %s", hostPath)
	}
	return trimmedPath[:separatorIndex], trimmedPath[separatorIndex+1:], nil
}

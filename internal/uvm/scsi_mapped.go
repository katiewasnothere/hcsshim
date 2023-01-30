//go:build windows

package uvm

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/Microsoft/hcsshim/internal/hcs/resourcepaths"
	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/protocol/guestrequest"
	"github.com/Microsoft/hcsshim/internal/protocol/guestresource"
)

// Release frees the resources of the corresponding Scsi Mount

func (gm *SCSIPartitionedGuestMount) Release(ctx context.Context) error {
	if err := gm.vm.RemovePartitionedSCSI(ctx, gm.scsiAttachment.HostPath, gm.uvmPath); err != nil {
		return fmt.Errorf("failed to remove SCSI mount: %s", err)
	}
	return nil
}

type SCSISlot struct {
	HostPath   string
	Controller int
	LUN        int32
}

// SCSIMount struct representing a SCSI mount point and the UVM
// it belongs to.
type SCSIAttachment struct {
	// path is the host path to the vhd that is mounted.
	HostPath string
	// scsi controller
	Controller int
	// scsi logical unit number
	LUN int32
	// While most VHDs attached to SCSI are scratch spaces, in the case of LCOW
	// when the size is over the size possible to attach to PMEM, we use SCSI for
	// read-only layers. As RO layers are shared, we perform ref-counting.
	isLayer  bool
	refCount uint32
	// specifies if this is an encrypted VHD
	encrypted bool
	// specifies if this is a readonly layer
	readOnly bool
	// "VirtualDisk" or "PassThru" or "ExtensibleVirtualDisk" disk attachment type.
	attachmentType string
	// If attachmentType is "ExtensibleVirtualDisk" then extensibleVirtualDiskType should
	// specify the type of it (for e.g "space" for storage spaces). Otherwise this should be
	// empty.
	extensibleVirtualDiskType string
	// serialization ID
	serialVersionID uint32
	// Make sure that serialVersionID is always the last field and its value is
	// incremented every time this structure is updated

	// A channel to wait on while mount of this SCSI disk is in progress.
	waitCh chan struct{}
	// The error field that is set if the mounting of this disk fails. Any other waiters on waitCh
	// can use this waitErr after the channel is closed.
	waitErr error
}

type SCSIPartitionedGuestMount struct {
	partition uint8
	uvmPath   string
	// used for cleaning up guest mounts
	// scsiAttachment *SCSIAttachment
	vm             *UtilityVM
	scsiAttachment *SCSIAttachment

	// partitioned mounts need their own ref count and wait channel since
	// we operate at the partitioned guest mount level
	refCount uint32

	// A channel to wait on while mount of this SCSI disk is in progress.
	waitCh chan struct{}
	// The error field that is set if the mounting of this disk fails. Any other waiters on waitCh
	// can use this waitErr after the channel is closed.
	waitErr error
}

func (gm *SCSIPartitionedGuestMount) UVMPath() string {
	return gm.uvmPath
}

func (gm *SCSIPartitionedGuestMount) RefCount() uint32 {
	return gm.refCount
}

// RefCount returns the current refcount for the SCSI mount.
func (sm *SCSIAttachment) RefCount() uint32 {
	return sm.refCount
}

func (sm *SCSIAttachment) logFormat() logrus.Fields {
	return logrus.Fields{
		"HostPath":                  sm.HostPath,
		"isLayer":                   sm.isLayer,
		"refCount":                  sm.refCount,
		"Controller":                sm.Controller,
		"LUN":                       sm.LUN,
		"ExtensibleVirtualDiskType": sm.extensibleVirtualDiskType,
		"SerialVersionID":           sm.serialVersionID,
	}
}

func (gm *SCSIPartitionedGuestMount) logFormat() logrus.Fields {
	return logrus.Fields{
		"UVMPath":   gm.uvmPath,
		"HostPath":  gm.scsiAttachment.HostPath,
		"Partition": gm.partition,
		"refCount":  gm.refCount,
	}
}

func newSCSIMount(
	uvm *UtilityVM,
	hostPath string,
	attachmentType string,
	evdType string,
	refCount uint32,
	controller int,
	lun int32,
	readOnly bool,
	encrypted bool,
) *SCSIAttachment {
	return &SCSIAttachment{
		// vm:                        uvm,
		HostPath:                  hostPath,
		refCount:                  refCount,
		Controller:                controller,
		LUN:                       int32(lun),
		encrypted:                 encrypted,
		readOnly:                  readOnly,
		attachmentType:            attachmentType,
		extensibleVirtualDiskType: evdType,
		serialVersionID:           scsiCurrentSerialVersionID,
		waitCh:                    make(chan struct{}),
	}
}

func newSCSIPartitionedGuestMount(
	vm *UtilityVM,
	scsiAttachment *SCSIAttachment,
	uvmPath string,
	partition uint8,
	refCount uint32,
) *SCSIPartitionedGuestMount {
	return &SCSIPartitionedGuestMount{
		vm: vm,
		// hostPath:  hostPath,
		scsiAttachment: scsiAttachment,
		uvmPath:        uvmPath,
		partition:      partition,
		refCount:       refCount,
	}
}

func (uvm *UtilityVM) deallocateSCSIPartitionedGuestMount(ctx context.Context, gm *SCSIPartitionedGuestMount) {
	uvm.m.Lock()
	defer uvm.m.Unlock()
	if gm != nil {
		log.G(ctx).WithFields(gm.logFormat()).Debug("removed SCSI guest location")
		// remove gm from sm
		uvm.scsiPartitionedMounts[gm.uvmPath] = nil
	}
}

func (uvm *UtilityVM) findSCSIPartitionedGuestMount(ctx context.Context, findThisUVMPath string) (*SCSIPartitionedGuestMount, error) {
	if gm, ok := uvm.scsiPartitionedMounts[findThisUVMPath]; ok {
		log.G(ctx).WithFields(gm.logFormat()).Debug("found SCSI Guest Mount")
		return gm, nil
	}
	return nil, ErrGuestMountNotFound
}

func (uvm *UtilityVM) removeSCSIAttachment(ctx context.Context, hostPath string) error {
	sm, err := uvm.findSCSIAttachment(ctx, hostPath)
	if err != nil {
		return err
	}
	sm.refCount--
	if sm.refCount > 0 {
		return nil
	}
	scsiModification := &hcsschema.ModifySettingRequest{}
	scsiModification.RequestType = guestrequest.RequestTypeRemove
	scsiModification.ResourcePath = fmt.Sprintf(resourcepaths.SCSIResourceFormat, guestrequest.ScsiControllerGuids[sm.Controller], sm.LUN)

	if err := uvm.modify(ctx, scsiModification); err != nil {
		return fmt.Errorf("failed to remove SCSI disk %s from container %s: %s", hostPath, uvm.id, err)
	}
	uvm.scsiLocations[sm.Controller][sm.LUN] = nil
	return nil
}

// must be called while holding the uvm lock
// must be called before we try to remove the scsi attachment so we can still access the
// scsi attachment info that we need
func (uvm *UtilityVM) removeSCSIPartitionedMount(ctx context.Context, hostPath, uvmPath string) error {
	// make sure the attachment still exists
	sm, err := uvm.findSCSIAttachment(ctx, hostPath)
	if err != nil {
		return err
	}

	// find the specific guest side mount we want to remove
	gm, err := uvm.findSCSIPartitionedGuestMount(ctx, uvmPath)
	if err != nil {
		return err
	}
	gm.refCount--
	if gm.refCount > 0 {
		return nil
	}

	// TODO katiewasnothere read super block of partitions?
	var verity *guestresource.DeviceVerityInfo
	if v, iErr := readVeritySuperBlock(ctx, sm.HostPath); iErr != nil {
		log.G(ctx).WithError(iErr).WithField("hostPath", sm.HostPath).Debug("unable to read dm-verity information from VHD")
	} else {
		if v != nil {
			log.G(ctx).WithFields(logrus.Fields{
				"hostPath":   sm.HostPath,
				"rootDigest": v.RootDigest,
			}).Debug("removing SCSI with dm-verity")
		}
		verity = v
	}

	scsiModification := &hcsschema.ModifySettingRequest{}

	// Include the GuestRequest so that the GCS ejects the disk cleanly if the
	// disk was attached/mounted
	//
	// Note: We always send a guest eject even if there is no UVM path in lcow
	// so that we synchronize the guest state. This seems to always avoid SCSI
	// related errors if this index quickly reused by another container.
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

	if err := uvm.modify(ctx, scsiModification); err != nil {
		return fmt.Errorf("failed to remove SCSI mount %s from container %s: %s", uvmPath, uvm.id, err)
	}
	uvm.scsiPartitionedMounts[gm.uvmPath] = nil
	return nil
}

// RemoveSCSI removes a SCSI disk from a utility VM.
func (uvm *UtilityVM) RemovePartitionedSCSI(ctx context.Context, hostPath, uvmPath string) error {
	uvm.m.Lock()
	defer uvm.m.Unlock()

	if uvm.scsiControllerCount == 0 {
		return ErrNoSCSIControllers
	}

	if err := uvm.removeSCSIPartitionedMount(ctx, hostPath, uvmPath); err != nil {
		return err
	}
	if err := uvm.removeSCSIAttachment(ctx, hostPath); err != nil {
		return err
	}
	return nil
}

// addSCSIActual is the implementation behind the external functions AddSCSI,
// AddSCSIPhysicalDisk, AddSCSIExtensibleVirtualDisk.
//
// We are in control of everything ourselves. Hence we have ref- counting and
// so-on tracking what SCSI locations are available or used.
//
// Returns result from calling modify with the given scsi mount
func (uvm *UtilityVM) addSCSIPartitionedActual(ctx context.Context, addReq *addSCSIRequest) (_ *SCSIPartitionedGuestMount, err error) {
	if uvm.operatingSystem == "windows" {
		return nil, ErrSCSIPartitionedWCWOUnsupported
	}
	sm, err := uvm.addSCSIAttachment(ctx, addReq)
	if err != nil {
		return nil, err
	}

	return uvm.addSCSIPartitionedGuestMount(ctx, addReq, sm)
}

func (uvm *UtilityVM) addSCSIAttachment(ctx context.Context, addReq *addSCSIRequest) (_ *SCSIAttachment, err error) {
	sm, smExisted, err := uvm.allocateSCSIAttachment(
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

	if smExisted {
		// another mount request might be in progress, wait for it to finish and if that operation
		// fails return that error.
		<-sm.waitCh
		if sm.waitErr != nil {
			return nil, sm.waitErr
		}
		return sm, nil
	}

	// This is the first goroutine to add this disk, close the waitCh after we are done.
	defer func() {
		if err != nil {
			uvm.deallocateSCSISlot(ctx, sm)
		}

		// error must be set _before_ the channel is closed.
		sm.waitErr = err
		close(sm.waitCh)
	}()

	SCSIModification := &hcsschema.ModifySettingRequest{}
	SCSIModification.RequestType = guestrequest.RequestTypeAdd
	SCSIModification.Settings = hcsschema.Attachment{
		Path:                      sm.HostPath,
		Type_:                     sm.attachmentType,
		ReadOnly:                  sm.readOnly,
		ExtensibleVirtualDiskType: sm.extensibleVirtualDiskType,
	}
	SCSIModification.ResourcePath = fmt.Sprintf(resourcepaths.SCSIResourceFormat, guestrequest.ScsiControllerGuids[sm.Controller], sm.LUN)

	if err := uvm.modify(ctx, SCSIModification); err != nil {
		return nil, fmt.Errorf("failed to modify UVM with new SCSI mount: %s", err)
	}
	return sm, nil
}

func (uvm *UtilityVM) addSCSIPartitionedGuestMount(ctx context.Context, addReq *addSCSIRequest, sm *SCSIAttachment) (_ *SCSIPartitionedGuestMount, err error) {
	gm, gmExisted, err := uvm.allocateSCSIPartitionedGuestMount(ctx, sm, addReq.uvmPath, addReq.partition)
	if err != nil {
		return nil, err
	}
	if gmExisted {
		// another mount request might be in progress, wait for it to finish and if that operation
		// fails return that error.
		<-gm.waitCh
		if gm.waitErr != nil {
			return nil, gm.waitErr
		}
		return gm, nil
	}

	// This is the first goroutine to add this disk, close the waitCh after we are done.
	defer func() {
		if err != nil {
			uvm.deallocateSCSIPartitionedGuestMount(ctx, gm)
		}

		// error must be set _before_ the channel is closed.
		gm.waitErr = err
		close(gm.waitCh)
	}()

	SCSIModification := &hcsschema.ModifySettingRequest{}
	if gm.uvmPath != "" {
		guestReq := guestrequest.ModificationRequest{
			ResourceType: guestresource.ResourceTypeMappedVirtualDisk,
			RequestType:  guestrequest.RequestTypeAdd,
		}

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
			ReadOnly:   sm.readOnly,
			Encrypted:  sm.encrypted,
			Options:    addReq.guestOptions,
			VerityInfo: verity,
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
func (uvm *UtilityVM) allocateSCSIPartitionedGuestMount(
	ctx context.Context,
	scsiAttachment *SCSIAttachment,
	uvmPath string,
	partition uint8,
) (*SCSIPartitionedGuestMount, bool, error) {
	// We must hold the lock throughout the lookup (findSCSIAttachment) until
	// after the possible allocation (allocateSCSISlot) has been completed to ensure
	// there isn't a race condition for it being attached by another thread between
	// these two operations.

	// TODO katiewasnothere: probably need this too right?
	uvm.m.Lock()
	defer uvm.m.Unlock()

	if gm, err := uvm.findSCSIPartitionedGuestMount(ctx, uvmPath); err == nil {
		// guest mount already exists, just increment the ref count
		gm.refCount++
		return gm, true, nil
	}

	// TODO katiewasnothere: check that partition matches too

	// guest mount doesn't exist, create a new one
	gm := newSCSIPartitionedGuestMount(uvm, scsiAttachment, uvmPath, partition, 1)
	uvm.scsiPartitionedMounts[uvmPath] = gm
	return gm, false, nil
}

// TODO katiewasnothere: fix this
// GetScsiUvmPath returns the guest mounted path of a SCSI drive.
//
// If `hostPath` is not mounted returns `ErrNotAttached`.
/*func (uvm *UtilityVM) GetScsiUvmPath(ctx context.Context, hostPath string) (string, error) {
	uvm.m.Lock()
	defer uvm.m.Unlock()
	sm, err := uvm.findSCSIAttachment(ctx, hostPath)
	if err != nil {
		return "", err
	}
	return sm.UVMPath, err
}*/

func (uvm *UtilityVM) GetSCSIAttachment(ctx context.Context, hostPath string) (*SCSIAttachment, error) {
	uvm.m.Lock()
	defer uvm.m.Unlock()
	sm, err := uvm.findSCSIAttachment(ctx, hostPath)
	if err != nil {
		return nil, err
	}
	return sm, err
}

func (uvm *UtilityVM) GetSCSIPartitionedGuestMount(ctx context.Context, uvmPath string) (*SCSIPartitionedGuestMount, error) {
	uvm.m.Lock()
	defer uvm.m.Unlock()
	gm, err := uvm.findSCSIPartitionedGuestMount(ctx, uvmPath)
	if err != nil {
		return nil, err
	}
	return gm, err
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
func (uvm *UtilityVM) AddPartitionedSCSI(
	ctx context.Context,
	hostPath string,
	uvmPath string,
	partition uint8,
	readOnly bool,
	encrypted bool,
	guestOptions []string,
	vmAccess VMAccessType,
) (*SCSIPartitionedGuestMount, error) {
	addReq := &addSCSIRequest{
		hostPath:       hostPath,
		uvmPath:        uvmPath,
		attachmentType: "VirtualDisk",
		readOnly:       readOnly,
		encrypted:      encrypted,
		guestOptions:   guestOptions,
		vmAccess:       vmAccess,
	}
	return uvm.addSCSIPartitionedActual(ctx, addReq)
}

// todo katiewasnothere: fix this
/*var _ = (Cloneable)(&SCSIGuestMount{})

// GobEncode serializes the SCSIMount struct
func (gm *SCSIGuestMount) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	encoder := gob.NewEncoder(&buf)
	errMsgFmt := "failed to encode SCSIMount: %s"
	// encode only the fields that can be safely deserialized.
	if err := encoder.Encode(gm.partition); err != nil {
		return nil, fmt.Errorf(errMsgFmt, err)
	}
	if err := encoder.Encode(gm.uvmPath); err != nil {
		return nil, fmt.Errorf(errMsgFmt, err)
	}
	if err := encoder.Encode(gm.vm); err != nil {
		return nil, fmt.Errorf(errMsgFmt, err)
	}
	if err := encoder.Encode(gm.hostPath); err != nil {
		return nil, fmt.Errorf(errMsgFmt, err)
	}
	if err := encoder.Encode(gm.refCount); err != nil {
		return nil, fmt.Errorf(errMsgFmt, err)
	}
	return buf.Bytes(), nil
}

func (gm *SCSIGuestMount) GobDecode(data []byte) error {
	buf := bytes.NewBuffer(data)
	decoder := gob.NewDecoder(buf)
	errMsgFmt := "failed to decode SCSIMount: %s"
	// fields should be decoded in the same order in which they were encoded.
	if err := decoder.Decode(&gm.partition); err != nil {
		return fmt.Errorf(errMsgFmt, err)
	}
	if err := decoder.Decode(&gm.uvmPath); err != nil {
		return fmt.Errorf(errMsgFmt, err)
	}
	if err := decoder.Decode(&gm.vm); err != nil {
		return fmt.Errorf(errMsgFmt, err)
	}
	if err := decoder.Decode(&gm.hostPath); err != nil {
		return fmt.Errorf(errMsgFmt, err)
	}
	if err := decoder.Decode(&gm.refCount); err != nil {
		return fmt.Errorf(errMsgFmt, err)
	}
	return nil
}*/

// TODO katiewasnothere: what the fuck
// Clone function creates a clone of the SCSIMount `sm` and adds the cloned SCSIMount to
// the uvm `vm`. If `sm` is read only then it is simply added to the `vm`. But if it is a
// writable mount(e.g a scratch layer) then a copy of it is made and that copy is added
// to the `vm`.
/*func (sm *SCSIGuestMount) Clone(ctx context.Context, vm *UtilityVM, cd *cloneData) error {
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
}*/

func (sm *SCSIAttachment) GetSerialVersionID() uint32 {
	return scsiCurrentSerialVersionID
}

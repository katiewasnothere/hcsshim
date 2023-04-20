package scsi

import (
	"context"
	"errors"
	"fmt"

	"github.com/Microsoft/hcsshim/internal/gcs"
	"github.com/Microsoft/hcsshim/internal/hcs"
	"github.com/Microsoft/hcsshim/internal/hcs/resourcepaths"
	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"
	"github.com/Microsoft/hcsshim/internal/protocol/guestrequest"
	"github.com/Microsoft/hcsshim/internal/protocol/guestresource"
)

// The concrete types here (not the Attacher/Mounter/Unplugger interfaces) would be a good option
// to move out to another package eventually. There is no real reason for them to live in
// the scsi package, and it could cause cyclical dependencies in the future.

// Attacher provides the low-level operations for attaching a SCSI device to a VM.
type Attacher interface {
	attach(ctx context.Context, controller, lun uint, config *attachConfig) error
	detach(ctx context.Context, controller, lun uint) error
}

// Mounter provides the low-level operations for mounting a SCSI device inside the guest OS.
type Mounter interface {
	mount(ctx context.Context, controller, lun uint, path string, config *mountConfig) error
	unmount(ctx context.Context, controller, lun uint, path string, config *mountConfig) error
}

// Unplugger provides the low-level operations for cleanly removing a SCSI device inside the guest OS.
type Unplugger interface {
	unplug(ctx context.Context, controller, lun uint) error
}

var _ Attacher = &hcsAttacher{}

type hcsAttacher struct {
	system *hcs.System
}

// NewHCSAttacher provides an [Attacher] using a [hcs.System].
func NewHCSAttacher(system *hcs.System) Attacher {
	return &hcsAttacher{system}
}

func (ha *hcsAttacher) attach(ctx context.Context, controller, lun uint, config *attachConfig) error {
	req := &hcsschema.ModifySettingRequest{
		RequestType: guestrequest.RequestTypeAdd,
		Settings: hcsschema.Attachment{
			Path:                      config.path,
			Type_:                     config.typ,
			ReadOnly:                  config.readOnly,
			ExtensibleVirtualDiskType: config.evdType,
		},
		ResourcePath: fmt.Sprintf(resourcepaths.SCSIResourceFormat, guestrequest.ScsiControllerGuids[controller], lun),
	}
	return ha.system.Modify(ctx, req)
}

func (ha *hcsAttacher) detach(ctx context.Context, controller, lun uint) error {
	req := &hcsschema.ModifySettingRequest{
		RequestType:  guestrequest.RequestTypeRemove,
		ResourcePath: fmt.Sprintf(resourcepaths.SCSIResourceFormat, guestrequest.ScsiControllerGuids[controller], lun),
	}
	return ha.system.Modify(ctx, req)
}

var _ Mounter = &bridgeMounter{}

type bridgeMounter struct {
	gc     *gcs.GuestConnection
	osType string
}

// NewBridgeMounter provides a [Mounter] using a [gcs.GuestConnection].
//
// osType should be either "windows" or "linux".
func NewBridgeMounter(gc *gcs.GuestConnection, osType string) Mounter {
	return &bridgeMounter{gc, osType}
}

func (bm *bridgeMounter) mount(ctx context.Context, controller, lun uint, path string, config *mountConfig) error {
	req, err := mountRequest(controller, lun, path, config, bm.osType)
	if err != nil {
		return err
	}
	return bm.gc.Modify(ctx, req)
}

func (bm *bridgeMounter) unmount(ctx context.Context, controller, lun uint, path string, config *mountConfig) error {
	req, err := unmountRequest(controller, lun, path, config, bm.osType)
	if err != nil {
		return err
	}
	return bm.gc.Modify(ctx, req)
}

var _ Mounter = &hcsMounter{}

type hcsMounter struct {
	system *hcs.System
	osType string
}

// NewHCSMounter provides a [Mounter] using a [hcs.System].
//
// osType should be either "windows" or "linux".
func NewHCSMounter(system *hcs.System, osType string) Mounter {
	return &hcsMounter{system, osType}
}

func (hm *hcsMounter) mount(ctx context.Context, controller, lun uint, path string, config *mountConfig) error {
	req, err := mountRequest(controller, lun, path, config, hm.osType)
	if err != nil {
		return err
	}
	return hm.system.Modify(ctx, &hcsschema.ModifySettingRequest{GuestRequest: req})
}

func (hm *hcsMounter) unmount(ctx context.Context, controller, lun uint, path string, config *mountConfig) error {
	req, err := unmountRequest(controller, lun, path, config, hm.osType)
	if err != nil {
		return err
	}
	return hm.system.Modify(ctx, &hcsschema.ModifySettingRequest{GuestRequest: req})
}

var _ Unplugger = &bridgeUnplugger{}

type bridgeUnplugger struct {
	gc     *gcs.GuestConnection
	osType string
}

// NewBridgeUnplugger provides an [Unplugger] using a [gcs.GuestConnection].
//
// osType should be either "windows" or "linux".
func NewBridgeUnplugger(gc *gcs.GuestConnection, osType string) Unplugger {
	return &bridgeUnplugger{gc, osType}
}

func (bu *bridgeUnplugger) unplug(ctx context.Context, controller, lun uint) error {
	req, err := unplugRequest(controller, lun, bu.osType)
	if err != nil {
		return err
	}
	if req.RequestType == "" {
		return nil
	}
	return bu.gc.Modify(ctx, req)
}

var _ Unplugger = &hcsUnplugger{}

type hcsUnplugger struct {
	system *hcs.System
	osType string
}

// NewHCSUnplugger provides an [Unplugger] using a [hcs.System].
//
// osType should be either "windows" or "linux".
func NewHCSUnplugger(system *hcs.System, osType string) Unplugger {
	return &hcsUnplugger{system, osType}
}

func (hu *hcsUnplugger) unplug(ctx context.Context, controller, lun uint) error {
	req, err := unplugRequest(controller, lun, hu.osType)
	if err != nil {
		return err
	}
	if req.RequestType == "" {
		return nil
	}
	return hu.system.Modify(ctx, &hcsschema.ModifySettingRequest{GuestRequest: req})
}

func mountRequest(controller, lun uint, path string, config *mountConfig, osType string) (guestrequest.ModificationRequest, error) {
	req := guestrequest.ModificationRequest{
		ResourceType: guestresource.ResourceTypeMappedVirtualDisk,
		RequestType:  guestrequest.RequestTypeAdd,
	}
	switch osType {
	case "windows":
		// We don't check config.readOnly here, as that will still result in the overall attachment being read-only.
		if controller != 0 {
			return guestrequest.ModificationRequest{}, errors.New("WCOW only supports SCSI controller 0")
		}
		if config.encrypted || config.verity != nil || len(config.options) != 0 {
			return guestrequest.ModificationRequest{}, errors.New("WCOW does not support encrypted, verity, or guest options on mounts")
		}
		req.Settings = guestresource.WCOWMappedVirtualDisk{
			ContainerPath: path,
			Lun:           int32(lun),
		}
	case "linux":
		req.Settings = guestresource.LCOWMappedVirtualDisk{
			MountPath:  path,
			Controller: uint8(controller),
			Lun:        uint8(lun),
			ReadOnly:   config.readOnly,
			Encrypted:  config.encrypted,
			Options:    config.options,
			VerityInfo: config.verity,
		}
	default:
		return guestrequest.ModificationRequest{}, fmt.Errorf("unsupported os type: %s", osType)
	}
	return req, nil
}

func unmountRequest(controller, lun uint, path string, config *mountConfig, osType string) (guestrequest.ModificationRequest, error) {
	req := guestrequest.ModificationRequest{
		ResourceType: guestresource.ResourceTypeMappedVirtualDisk,
		RequestType:  guestrequest.RequestTypeRemove,
	}
	switch osType {
	case "windows":
		req.Settings = guestresource.WCOWMappedVirtualDisk{
			ContainerPath: path,
			Lun:           int32(lun),
		}
	case "linux":
		req.Settings = guestresource.LCOWMappedVirtualDisk{
			MountPath:  path,
			Lun:        uint8(lun),
			Controller: uint8(controller),
			VerityInfo: config.verity,
		}
	default:
		return guestrequest.ModificationRequest{}, fmt.Errorf("unsupported os type: %s", osType)
	}
	return req, nil
}

func unplugRequest(controller, lun uint, osType string) (guestrequest.ModificationRequest, error) {
	var req guestrequest.ModificationRequest
	switch osType {
	case "windows":
		// Windows doesn't support an unplug operation, so treat as no-op.
	case "linux":
		req = guestrequest.ModificationRequest{
			ResourceType: guestresource.ResourceTypeSCSIDevice,
			RequestType:  guestrequest.RequestTypeRemove,
			Settings: guestresource.SCSIDevice{
				Controller: uint8(controller),
				Lun:        uint8(lun),
			},
		}
	default:
		return guestrequest.ModificationRequest{}, fmt.Errorf("unsupported os type: %s", osType)
	}
	return req, nil
}

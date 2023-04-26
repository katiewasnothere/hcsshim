package scsi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/protocol/guestresource"
	"github.com/Microsoft/hcsshim/internal/verity"
	"github.com/sirupsen/logrus"
)

var (
	// ErrNoAvailableLocation indicates that a new SCSI attachment failed because
	// no new slots were available.
	ErrNoAvailableLocation = errors.New("no available location")
	// ErrNotInitialized is returned when a method is invoked on a nil [Manager].
	ErrNotInitialized = errors.New("SCSI manager not initialized")
	// ErrAlreadyReleased is returned when [Mount.Release] is called on a Mount
	// that had already been released.
	ErrAlreadyReleased = errors.New("mount was already released")
)

// Manager is the primary entrypoint for managing SCSI devices on a VM.
// It tracks the state of what devices have been attached to the VM, and
// mounted inside the guest OS.
type Manager struct {
	attachManager *attachManager
	mountManager  *mountManager
}

// Slot represents a single SCSI slot, consisting of a controller and LUN.
type Slot struct {
	Controller uint
	LUN        uint
}

// NewManager creates a new Manager using the provided backend [Attacher],
// [Mounter], and [Unplugger], as well as other configuration parameters.
//
// guestMountFmt is the format string to use for mounts of SCSI devices in
// the guest OS. It should have a single %d format parameter.
//
// reservedSlots indicates which SCSI slots to treat as already used. They
// will not be handed out again by the Manager.
func NewManager(
	attacher Attacher,
	mounter Mounter,
	unplugger Unplugger,
	numControllers int,
	numLUNsPerController int,
	guestMountFmt string,
	reservedSlots []Slot,
) *Manager {
	am := newAttachManager(attacher, unplugger, numControllers, numLUNsPerController, reservedSlots)
	mm := newMountManager(mounter, guestMountFmt)
	return &Manager{am, mm}
}

// Mount represents a SCSI device that has been attached to a VM, and potentially
// also mounted into the guest OS.
type Mount struct {
	mgr         *Manager
	controller  uint
	lun         uint
	guestPath   string
	releaseOnce sync.Once
}

// Controller returns the controller number that the SCSI device is attached to.
func (m *Mount) Controller() uint {
	return m.controller
}

// LUN returns the LUN number that the SCSI device is attached to.
func (m *Mount) LUN() uint {
	return m.lun
}

// GuestPath returns the path inside the guest OS where the SCSI device was mounted.
// Will return an empty string if no guest mount was performed.
func (m *Mount) GuestPath() string {
	return m.guestPath
}

// Release releases the SCSI mount. Refcount tracking is used in case multiple instances
// of the same attachment or mount are used. If the refcount for the guest OS mount
// reaches 0, the guest OS mount is removed. If the refcount for the SCSI attachment
// reaches 0, the SCSI attachment is removed.
func (m *Mount) Release(ctx context.Context) (err error) {
	err = ErrAlreadyReleased
	m.releaseOnce.Do(func() {
		err = m.mgr.remove(ctx, m.controller, m.lun, m.guestPath)
	})
	return
}

func (m *Manager) Add(ctx context.Context, attachConfig *AttachConfig, mountConfig *MountConfig) (_ *Mount, err error) {
	if m == nil {
		return nil, ErrNotInitialized
	}
	controller, lun, err := m.attachManager.attach(ctx, attachConfig)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_, _ = m.attachManager.detach(ctx, controller, lun)
		}
	}()

	var guestPath string
	if mountConfig != nil {
		guestPath, err = m.mountManager.mount(ctx, controller, lun, mountConfig)
		if err != nil {
			return nil, err
		}
	}

	return &Mount{mgr: m, controller: controller, lun: lun, guestPath: guestPath}, nil
}

func (m *Manager) remove(ctx context.Context, controller, lun uint, guestPath string) error {
	if guestPath != "" {
		removed, err := m.mountManager.unmount(ctx, guestPath)
		if err != nil {
			return err
		}

		if !removed {
			return nil
		}
	}

	if _, err := m.attachManager.detach(ctx, controller, lun); err != nil {
		return err
	}

	return nil
}

func ReadVerityInfo(ctx context.Context, path string) *guestresource.DeviceVerityInfo {
	if v, iErr := verity.ReadVeritySuperBlock(ctx, path); iErr != nil {
		log.G(ctx).WithError(iErr).WithField("hostPath", path).Debug("unable to read dm-verity information from VHD")
	} else {
		if v != nil {
			log.G(ctx).WithFields(logrus.Fields{
				"hostPath":   path,
				"rootDigest": v.RootDigest,
			}).Debug("adding SCSI with dm-verity")
		}
		return v
	}
	return nil
}

// ParseExtensibleVirtualDiskPath parses the evd path provided in the config.
// extensible virtual disk path has format "evd://<evdType>/<evd-mount-path>"
// this function parses that and returns the `evdType` and `evd-mount-path`.
func ParseExtensibleVirtualDiskPath(hostPath string) (evdType, mountPath string, err error) {
	trimmedPath := strings.TrimPrefix(hostPath, "evd://")
	separatorIndex := strings.Index(trimmedPath, "/")
	if separatorIndex <= 0 {
		return "", "", fmt.Errorf("invalid extensible vhd path: %s", hostPath)
	}
	return trimmedPath[:separatorIndex], trimmedPath[separatorIndex+1:], nil
}

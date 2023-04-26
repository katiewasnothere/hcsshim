package scsi

import (
	"context"
	"fmt"
	"sync"
)

type attachManager struct {
	m                    sync.Mutex
	attacher             Attacher
	unplugger            Unplugger
	numControllers       int
	numLUNsPerController int
	slots                [][]*attachment
}

func newAttachManager(attacher Attacher, unplugger Unplugger, numControllers, numLUNsPerController int, reservedSlots []Slot) *attachManager {
	slots := make([][]*attachment, numControllers)
	for i := range slots {
		slots[i] = make([]*attachment, numLUNsPerController)
	}
	for _, reservedSlot := range reservedSlots {
		// Mark the slot as already filled so we don't try to re-use it.
		// The nil attachConfig should mean it never matches a prospective new attach.
		// The refCount of 1 should not strictly be needed, since we will never get a
		// remove call for this slot, but is done for added safety.
		slots[reservedSlot.Controller][reservedSlot.LUN] = &attachment{refCount: 1}
	}
	return &attachManager{
		attacher:             attacher,
		unplugger:            unplugger,
		numControllers:       numControllers,
		numLUNsPerController: numLUNsPerController,
		slots:                slots,
	}
}

type attachment struct {
	controller uint
	lun        uint
	config     *AttachConfig
	waitErr    error
	waitCh     chan struct{}
	refCount   uint
}

type AttachConfig struct {
	Path     string
	ReadOnly bool
	Type     AttachmentType
	EVDType  string // Only valid if Type is AttachmentTypeExtensibleVirtualDisk.
}

type AttachmentType string

const (
	AttachmentTypeVirtualDisk           AttachmentType = "VirtualDisk"
	AttachmentTypePassThru              AttachmentType = "PassThru"
	AttachmentTypeExtensibleVirtualDisk AttachmentType = "ExtensibleVirtualDisk"
)

func (am *attachManager) attach(ctx context.Context, c *AttachConfig) (controller uint, lun uint, err error) {
	att, existed, err := am.trackAttachment(c)
	if err != nil {
		return 0, 0, err
	}
	if existed {
		<-att.waitCh
		if att.waitErr != nil {
			return 0, 0, att.waitErr
		}
		return att.controller, att.lun, nil
	}

	defer func() {
		if err != nil {
			am.m.Lock()
			am.untrackAttachment(att)
			am.m.Unlock()
		}

		att.waitErr = err
		close(att.waitCh)
	}()

	if err := am.attacher.attach(ctx, att.controller, att.lun, att.config); err != nil {
		return 0, 0, fmt.Errorf("attach %s/%s at controller %d lun %d: %w", att.config.Type, att.config.Path, att.controller, att.lun, err)
	}
	return att.controller, att.lun, nil
}

func (am *attachManager) detach(ctx context.Context, controller, lun uint) (bool, error) {
	am.m.Lock()
	defer am.m.Unlock()

	if controller >= uint(am.numControllers) || lun >= uint(am.numLUNsPerController) {
		return false, fmt.Errorf("controller %d lun %d out of range", controller, lun)
	}

	att := am.slots[controller][lun]
	att.refCount--
	if att.refCount > 0 {
		return false, nil
	}

	if err := am.unplugger.unplug(ctx, controller, lun); err != nil {
		return false, fmt.Errorf("unplug controller %d lun %d: %w", controller, lun, err)
	}
	if err := am.attacher.detach(ctx, controller, lun); err != nil {
		return false, fmt.Errorf("detach controller %d lun %d: %w", controller, lun, err)
	}

	am.untrackAttachment(att)

	return true, nil
}

func (am *attachManager) trackAttachment(c *AttachConfig) (*attachment, bool, error) {
	am.m.Lock()
	defer am.m.Unlock()

	var (
		freeController int = -1
		freeLUN        int = -1
	)
	for controller := range am.slots {
		for lun := range am.slots[controller] {
			attachment := am.slots[controller][lun]
			if attachment == nil {
				if freeController == -1 {
					freeController = controller
					freeLUN = lun
				}
			} else if c == attachment.config {
				attachment.refCount++
				return attachment, true, nil
			}
		}
	}

	if freeController == -1 {
		return nil, false, ErrNoAvailableLocation
	}

	// New attachment.
	attachment := &attachment{
		controller: uint(freeController),
		lun:        uint(freeLUN),
		config:     c,
		refCount:   1,
		waitCh:     make(chan struct{}),
	}
	am.slots[freeController][freeLUN] = attachment
	return attachment, false, nil
}

// Caller must be holding am.m.
func (am *attachManager) untrackAttachment(attachment *attachment) {
	am.slots[attachment.controller][attachment.lun] = nil
}

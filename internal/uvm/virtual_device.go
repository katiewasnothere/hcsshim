package uvm

import (
	"context"
	"fmt"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/internal/guestrequest"
	"github.com/Microsoft/hcsshim/internal/requesttype"
	hcsschema "github.com/Microsoft/hcsshim/internal/schema2"
)

func (uvm *UtilityVM) AssignDevice(ctx context.Context, device hcsschema.VirtualPciDevice) (string, error) {
	guid, err := guid.NewV4()
	if err != nil {
		return "", err
	}
	id := guid.String()

	// TODO katiewasnothere: is there anything on the guest side to be done for
	// assigning a device to the pod?
	guestRequest := guestrequest.GuestRequest{}
	if uvm.operatingSystem != "windows" {
		guestRequest = guestrequest.GuestRequest{
			ResourceType: guestrequest.ResourceTypeVPCIDevice,
			RequestType:  requesttype.Add,
			Settings: guestrequest.LCOWMappedVPCIDevice{
				VMBusGUID: id,
			},
		}
	}

	uvm.m.Lock()
	defer uvm.m.Unlock()
	return id, uvm.modify(ctx, &hcsschema.ModifySettingRequest{
		ResourcePath: fmt.Sprintf(virtualPciResourceFormat, id),
		RequestType:  requesttype.Add,
		Settings:     device,
		GuestRequest: guestRequest,
	})
}

func (uvm *UtilityVM) RemoveDevice(ctx context.Context, id string) error {
	uvm.m.Lock()
	defer uvm.m.Unlock()
	return uvm.modify(ctx, &hcsschema.ModifySettingRequest{
		ResourcePath: fmt.Sprintf(virtualPciResourceFormat, id),
		RequestType:  requesttype.Remove,
	})
}

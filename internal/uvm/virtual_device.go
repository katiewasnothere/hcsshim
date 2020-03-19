package uvm

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/internal/guestrequest"
	"github.com/Microsoft/hcsshim/internal/requesttype"
	hcsschema "github.com/Microsoft/hcsshim/internal/schema2"
)

const VPCIDeviceIDType = "vpci"
const VPCILocationPathIDType = "LocationPath"

// this is the well known channel type GUID for all assigned devices
const channelTypeGUIDFormatted = "{44c4f61d-4444-4400-9d52-802e27ede19f}"
const assignedDeviceEnumerator = "VMBUS"

// A vpci bus's instance ID is in the form: "VMBUS\channelTypeGUIDFormatted\{vmBusInstanceGUID}"
func (uvm *UtilityVM) GetAssignedDeviceParentID(vmBusInstanceGUID string) string {
	formattedInstanceGUID := fmt.Sprintf("{%s}", vmBusInstanceGUID)
	return filepath.Join(assignedDeviceEnumerator, channelTypeGUIDFormatted, formattedInstanceGUID)
}

func (uvm *UtilityVM) AssignDevice(ctx context.Context, device hcsschema.VirtualPciDevice) (string, error) {
	guid, err := guid.NewV4()
	if err != nil {
		return "", err
	}
	id := guid.String()
	req := &hcsschema.ModifySettingRequest{
		ResourcePath: fmt.Sprintf(virtualPciResourceFormat, id),
		RequestType:  requesttype.Add,
		Settings:     device,
	}
	if uvm.operatingSystem != "windows" {
		req.GuestRequest = guestrequest.GuestRequest{
			ResourceType: guestrequest.ResourceTypeVPCIDevice,
			RequestType:  requesttype.Add,
			Settings: guestrequest.LCOWMappedVPCIDevice{
				VMBusGUID: id,
			},
		}
	}
	uvm.m.Lock()
	defer uvm.m.Unlock()
	return id, uvm.modify(ctx, req)
}

func (uvm *UtilityVM) RemoveDevice(ctx context.Context, id string) error {
	uvm.m.Lock()
	defer uvm.m.Unlock()
	return uvm.modify(ctx, &hcsschema.ModifySettingRequest{
		ResourcePath: fmt.Sprintf(virtualPciResourceFormat, id),
		RequestType:  requesttype.Remove,
	})
}

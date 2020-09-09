package uvm

import (
	"context"
	"fmt"

	hcsschema "github.com/Microsoft/hcsshim/internal/schema2"
)

const (
	bytesPerPage = 4096
	bytesPerMB   = 1024 * 1024
)

// UpdateMemory makes a call to the VM's orchestrator to update the VM's size in MB
func (uvm *UtilityVM) UpdateMemory(ctx context.Context, sizeInMB uint64) error {
	req := &hcsschema.ModifySettingRequest{
		ResourcePath: memoryResourcePath,
		Settings:     sizeInMB,
	}
	if err := uvm.modify(ctx, req); err != nil {
		return err
	}
	return nil
}

// GetAssignedMemoryInMB returns the amount of assigned memory for the UVM in MBs
func (uvm *UtilityVM) GetAssignedMemoryInMB(ctx context.Context) (uint64, error) {
	props, err := uvm.hcsSystem.PropertiesV2(ctx, hcsschema.PTMemory)
	if err != nil {
		return 0, err
	}
	if props.Memory == nil {
		return 0, fmt.Errorf("no memory properties returned for system %s", uvm.id)
	}
	if props.Memory.VirtualMachineMemory == nil {
		return 0, fmt.Errorf("no virtual memory properties returned for system %s", uvm.id)
	}
	pages := props.Memory.VirtualMachineMemory.AssignedMemory
	if pages == 0 {
		return 0, fmt.Errorf("assigned memory returned should not be 0 for system %s", uvm.id)
	}
	memInMB := (pages * bytesPerPage) / bytesPerMB
	return memInMB, nil
}

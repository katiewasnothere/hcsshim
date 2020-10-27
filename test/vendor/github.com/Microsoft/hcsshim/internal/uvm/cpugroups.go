package uvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Microsoft/hcsshim/internal/hcs"
	"github.com/Microsoft/hcsshim/internal/log"
	hcsschema "github.com/Microsoft/hcsshim/internal/schema2"
)

const cpuGroupNullID = "00000000-0000-0000-0000-000000000000"

var _HV_STATUS_INVALID_CPU_GROUP_STATE = errors.New("The hypervisor could not perform the operation because the CPU group is entering or in an invalid state.")

// ReleaseCPUGroup unsets the cpugroup from the VM and attemps to delete it
func (uvm *UtilityVM) ReleaseCPUGroup(ctx context.Context) error {
	groupID := uvm.cpuGroupID
	if groupID == "" {
		// not set, don't try to do anything
		return nil
	}
	if err := uvm.unsetCPUGroup(ctx); err != nil {
		return fmt.Errorf("failed to remove VM %s from cpugroup %s", uvm.ID(), groupID)
	}

	err := deleteCPUGroup(ctx, groupID)
	if err != nil && err == _HV_STATUS_INVALID_CPU_GROUP_STATE {
		log.G(ctx).WithField("error", err).Warn("cpugroup could not be deleted, other VMs may be in this group")
		return nil
	}
	return err
}

// ConfigureVMCPUGroup setups up the cpugroup for the VM with the requested id
func (uvm *UtilityVM) ConfigureVMCPUGroup(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("must specify an ID to use when configuring a VM's cpugroup")
	}
	exists, err := cpuGroupExists(ctx, id)
	if err != nil {
		return err
	}

	if !exists {
		return fmt.Errorf("no cpugroup with ID %v exists on the host", id)
	}

	return uvm.setCPUGroup(ctx, id)
}

// setCPUGroup sets the VM's cpugroup
func (uvm *UtilityVM) setCPUGroup(ctx context.Context, id string) error {
	req := &hcsschema.ModifySettingRequest{
		ResourcePath: cpuGroupResourcePath,
		Settings: &hcsschema.CpuGroup{
			Id: id,
		},
	}
	if err := uvm.modify(ctx, req); err != nil {
		return err
	}
	uvm.cpuGroupID = id
	return nil
}

// unsetCPUGroup sets the VM's cpugroup to the null group ID
// set groupID to 00000000-0000-0000-0000-000000000000 to remove the VM from a cpugroup
//
// Since a VM must be moved to the null group before potentially being added to a different
// cpugroup, that means there may be a segment of time that the VM's cpu usage runs unrestricted.
func (uvm *UtilityVM) unsetCPUGroup(ctx context.Context) error {
	return uvm.setCPUGroup(ctx, cpuGroupNullID)
}

// DeleteCPUGroup deletes the cpugroup from the host
func deleteCPUGroup(ctx context.Context, id string) error {
	operation := hcsschema.DeleteGroup
	details := hcsschema.DeleteGroupOperation{
		GroupId: id,
	}

	return modifyCPUGroupRequest(ctx, operation, details)
}

// modifyCPUGroupRequest is a helper function for making modify calls to a cpugroup
func modifyCPUGroupRequest(ctx context.Context, operation hcsschema.CPUGroupOperation, details interface{}) error {
	req := hcsschema.ModificationRequest{
		PropertyType: hcsschema.PTCPUGroup,
		Settings: &hcsschema.HostProcessorModificationRequest{
			Operation:        operation,
			OperationDetails: details,
		},
	}

	return hcs.ModifyServiceSettings(ctx, req)
}

// CreateNewCPUGroupWithID creates a new cpugroup on the host with a prespecified id
func CreateNewCPUGroupWithID(ctx context.Context, id string, logicalProcessors []uint32) error {
	operation := hcsschema.CreateGroup
	details := &hcsschema.CreateGroupOperation{
		GroupId:               strings.ToLower(id),
		LogicalProcessors:     logicalProcessors,
		LogicalProcessorCount: uint32(len(logicalProcessors)),
	}
	if err := modifyCPUGroupRequest(ctx, operation, details); err != nil {
		return fmt.Errorf("failed to make cpugroups CreateGroup request with details %v with: %s", details, err)
	}
	return nil
}

// getHostCPUGroups queries the host for cpugroups and their properties.
func getHostCPUGroups(ctx context.Context) (*hcsschema.CpuGroupConfigurations, error) {
	query := hcsschema.PropertyQuery{
		PropertyTypes: []hcsschema.PropertyType{hcsschema.PTCPUGroup},
	}

	cpuGroupsPresent, err := hcs.GetServiceProperties(ctx, query)
	if err != nil {
		return nil, err
	}

	groupConfigs := &hcsschema.CpuGroupConfigurations{}
	if err := json.Unmarshal(cpuGroupsPresent.Properties[0], groupConfigs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal host cpugroups: %v", err)
	}

	return groupConfigs, nil
}

// getCPUGroupConfig finds the cpugroup config information for group with `id`
func getCPUGroupConfig(ctx context.Context, id string) (*hcsschema.CpuGroupConfig, error) {
	groupConfigs, err := getHostCPUGroups(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range groupConfigs.CpuGroups {
		if strings.ToLower(c.GroupId) == strings.ToLower(id) {
			return &c, nil
		}
	}
	return nil, nil
}

// cpuGroupExists is a helper fucntion to determine if cpugroup with `id` exists
// already on the host.
func cpuGroupExists(ctx context.Context, id string) (bool, error) {
	groupConfig, err := getCPUGroupConfig(ctx, id)
	if err != nil {
		return false, err
	}

	return groupConfig != nil, nil
}

package uvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/internal/hcs"
	"github.com/Microsoft/hcsshim/internal/log"
	hcsschema "github.com/Microsoft/hcsshim/internal/schema2"
)

const (
	CPUGroupNullID = "00000000-0000-0000-0000-000000000000"
	MaxCPUGroupCap = 65536

	// Default values in host for cpugroups
	DefaultCPUGroupCap      = MaxCPUGroupCap
	DefaultCPUGroupPriority = 1
)

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

// CPUGroupOptions is used to construct the various options for setting up/creating
// a cpugroup for a UVM.
type CPUGroupOptions struct {
	CreateRandomID    bool
	ID                string
	LogicalProcessors []uint32
	Cap               *uint32
	Priority          *uint32
}

// verifyCPUGroupOptions verifies that the CPUGroupOptions are a valid cpugroup configuration
func verifyCPUGroupOptions(opts *CPUGroupOptions) error {
	if opts.CreateRandomID && opts.ID != CPUGroupNullID {
		return fmt.Errorf("cannot use a specific cpugroup ID when the `CreateRandomID` option is set")
	}
	if len(opts.LogicalProcessors) == 0 || opts.LogicalProcessors == nil {
		return fmt.Errorf("must specify the logical processors to use when creating a cpugroup")
	}
	return nil
}

// ConfigureVMCPUGroup parses the CPUGroupOptions `opts` and setups up the cpugroup for the VM
// with the requested settings.
//
// If CreateRandomID is set, ignore any potential group ID that was also set on the opts
func (uvm *UtilityVM) ConfigureVMCPUGroup(ctx context.Context, opts *CPUGroupOptions) error {
	if err := verifyCPUGroupOptions(opts); err != nil {
		return err
	}
	if opts.CreateRandomID {
		id, err := guid.NewV4()
		if err != nil {
			return err
		}
		opts.ID = id.String()
		log.G(ctx).WithField("id", id.String()).Info("Created random id")
	}
	exists, err := cpuGroupExists(ctx, opts.ID)
	if err != nil {
		return err
	}

	if !exists {
		log.G(ctx).WithField("lps", opts.LogicalProcessors).Info("the cpugroup does not exist")
		if err := createNewCPUGroupWithID(ctx, opts.ID, opts.LogicalProcessors); err != nil {
			return err
		}
	}

	if err := updateCPUGroupProperties(ctx, opts.ID, opts.Cap, opts.Priority); err != nil {
		return err
	}
	return uvm.setCPUGroup(ctx, opts.ID)
}

func updateCPUGroupProperties(ctx context.Context, id string, cap, priority *uint32) error {
	config, err := getCPUGroupConfig(ctx, id)
	if err != nil {
		return err
	}
	previousCap, err := getCPUGroupCapFromConfig(ctx, id, config)
	if err != nil {
		return err
	}
	previousPri, err := getCPUGroupPriorityFromConfig(ctx, id, config)
	if err != nil {
		return err
	}
	if cap != nil && *cap != previousCap {
		log.G(ctx).WithField("cap", cap).Info("Updating cap of cpugroup")
		if err := setCPUGroupCap(ctx, id, *cap); err != nil {
			return err
		}
	}
	if priority != nil && *priority != previousPri {
		log.G(ctx).WithField("pri", priority).Info("Updating priority of cpugroup")

		if err := setCPUGroupSchedulingPriority(ctx, id, *priority); err != nil {
			return err
		}
	}
	return nil
}

// ModifyVMCPUGroup is used to modify the VM's cpugroup during runtime
func (uvm *UtilityVM) ModifyVMCPUGroup(ctx context.Context, opts *CPUGroupOptions) error {
	if opts.CreateRandomID || opts.ID != "" {
		// modify request changes the cpugroup that the VM should be in, so release the previous group
		if err := uvm.ReleaseCPUGroup(ctx); err != nil {
			return err
		}
	}
	return uvm.ConfigureVMCPUGroup(ctx, opts)
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
	log.G(ctx).WithField("previous group id", uvm.cpuGroupID).Debug("unsetting the VM's CPU Group")
	return uvm.setCPUGroup(ctx, CPUGroupNullID)
}

// deleteCPUGroup deletes the cpugroup from the host
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

// createNewCPUGroupWithID creates a new cpugroup on the host with a prespecified id
func createNewCPUGroupWithID(ctx context.Context, id string, logicalProcessors []uint32) error {
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

// setCPUGroupCap sets the cpugroup cap.
// Param `cap` must be an integer in the range [0, 65536]. A `cap` value of 32768 = 50% group cap.
func setCPUGroupCap(ctx context.Context, id string, cap uint32) error {
	if cap > MaxCPUGroupCap {
		return fmt.Errorf("cpugroup cap must be between [0, %d] inclusive, tried to use a cap of %d for group %v", MaxCPUGroupCap, cap, id)
	}

	operation := hcsschema.SetProperty
	details := hcsschema.SetPropertyOperation{
		GroupId:       id,
		PropertyCode:  hcsschema.CpuCapPropertyCode,
		PropertyValue: cap,
	}
	if err := modifyCPUGroupRequest(ctx, operation, details); err != nil {
		return fmt.Errorf("failed to make cpugroups SetProperty request with details %v with: %s", details, err)
	}

	return nil
}

// setCPUGroupSchedulingPriority sets the cpugroup's scheduling priority
func setCPUGroupSchedulingPriority(ctx context.Context, id string, priority uint32) error {
	operation := hcsschema.SetProperty
	details := &hcsschema.SetPropertyOperation{
		GroupId:       id,
		PropertyCode:  hcsschema.SchedulingPriorityPropertyCode,
		PropertyValue: priority,
	}

	if err := modifyCPUGroupRequest(ctx, operation, details); err != nil {
		return fmt.Errorf("failed to make cpugroups SetProperty request with details %v with: %s", details, err)
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

// getCPUGroupCap is a helper function to return the group cpu capacity of
// cpugroup with `id`
func getCPUGroupCap(ctx context.Context, id string) (uint32, error) {
	config, err := getCPUGroupConfig(ctx, id)
	if err != nil {
		return 0, err
	}
	return getCPUGroupCapFromConfig(ctx, id, config)
}

func getCPUGroupCapFromConfig(ctx context.Context, id string, config *hcsschema.CpuGroupConfig) (uint32, error) {
	props := config.GroupProperties
	for _, p := range props {
		if p.PropertyCode == hcsschema.CpuCapPropertyCode {
			return p.PropertyValue, nil
		}
	}
	return 0, fmt.Errorf("failed to get cpu cap property information for cpugroup %s", id)
}

func getCPUGroupPriorityFromConfig(ctx context.Context, id string, config *hcsschema.CpuGroupConfig) (uint32, error) {
	props := config.GroupProperties
	for _, p := range props {
		if p.PropertyCode == hcsschema.SchedulingPriorityPropertyCode {
			return p.PropertyValue, nil
		}
	}
	return 0, fmt.Errorf("failed to get cpu priority property information for cpugroup %s", id)
}

// getCPUGroupPriority is a helper function to return the group scheduling priority of
// cpugroup with `id`
func getCPUGroupPriority(ctx context.Context, id string) (uint32, error) {
	config, err := getCPUGroupConfig(ctx, id)
	if err != nil {
		return 0, err
	}
	return getCPUGroupPriorityFromConfig(ctx, id, config)
}

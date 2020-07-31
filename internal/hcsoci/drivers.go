// +build windows

package hcsoci

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Microsoft/hcsshim/internal/oci"
	"github.com/Microsoft/hcsshim/internal/uvm"
	"github.com/pkg/errors"
)

// getAssignedDeviceUtilityTool gets the path to device-util on the server host
func getAssignedDeviceUtilityTool(coi *createOptionsInternal) (string, error) {
	tools, ok := coi.Spec.Annotations[oci.AnnotationAssignedDeviceUtilityTool]
	if !ok || tools == "" {
		return "", fmt.Errorf("no driver tools were specified for %s", coi.actualID)
	}
	if _, err := os.Stat(tools); err != nil {
		return "", errors.Wrapf(err, "failed to find device installation tools at %s", tools)
	}
	return tools, nil
}

// getAssignedDeviceKernelDrivers gets any device drivers specified on the spec.
// Drivers are optional, therefore do not return an error if none are on the spec.
func getAssignedDeviceKernelDrivers(coi *createOptionsInternal) ([]string, error) {
	csDrivers, ok := coi.Spec.Annotations[oci.AnnotationAssignedDeviceKernelDrivers]
	if !ok || csDrivers == "" {
		return nil, nil
	}
	drivers := strings.Split(csDrivers, ",")
	for _, driver := range drivers {
		if _, err := os.Stat(driver); err != nil {
			return nil, errors.Wrapf(err, "failed to find path to drivers at %s", driver)
		}
	}
	return drivers, nil
}

// setupDeviceUtilTool finds the utility tool's host path, mounts it using vsmb, and
// returns the UVM path to the tools
func setupDeviceUtilTool(ctx context.Context, coi *createOptionsInternal, r *Resources) (string, error) {
	toolHostPath, err := getAssignedDeviceUtilityTool(coi)
	if err != nil {
		return "", err
	}
	return addVSMBToUVM(ctx, coi.HostingSystem, r, toolHostPath)
}

// installWindowsDrivers finds specified kernel driver directories, mounts them using vsmb,
// then installs them in the UVM
func installWindowsDrivers(ctx context.Context, coi *createOptionsInternal, resources *Resources) error {
	drivers, err := getAssignedDeviceKernelDrivers(coi)
	if err != nil {
		return err
	}
	if drivers == nil {
		// no drivers were specified, skip installing drivers
		return nil
	}
	driverUVMPaths, err := mountDrivers(ctx, coi.HostingSystem, resources, drivers)
	if err != nil {
		return err
	}
	return execPnPInstallAllDrivers(ctx, coi.HostingSystem, driverUVMPaths)
}

// mountDrivers mounts all specified driver files using VSMB and returns their path
// in the UVM
func mountDrivers(ctx context.Context, vm *uvm.UtilityVM, r *Resources, hostPaths []string) (resultUVMPaths []string, err error) {
	for _, d := range hostPaths {
		uvmPath, err := addVSMBToUVM(ctx, vm, r, d)
		if err != nil {
			return nil, err
		}
		resultUVMPaths = append(resultUVMPaths, uvmPath)
	}
	return resultUVMPaths, nil
}

func addVSMBToUVM(ctx context.Context, vm *uvm.UtilityVM, r *Resources, hostPath string) (string, error) {
	options := vm.DefaultVSMBOptions(true)
	share, err := vm.AddVSMB(ctx, hostPath, options)
	if err != nil {
		return "", fmt.Errorf("failed to add VSMB share to utility VM for path %+v: %s", hostPath, err)
	}
	r.resources = append(r.resources, share)
	return vm.GetVSMBUvmPath(ctx, hostPath, true)
}

// createDeviceUtilChildrenCommand constructs a device-util command to query the UVM for
// device information
//
// `deviceUtilPath` is the UVM path to device-util
//
// `vmBusInstanceIDs` is a slice of vmbus instance IDs already assigned to the UVM
//
// Returns a slice of strings that represent the location paths in the UVM of the
// target devices
func createDeviceUtilChildrenCommand(deviceUtilPath string, vmBusInstanceIDs []string) []string {
	joinedVMBusIDs := strings.Join(vmBusInstanceIDs, ",")
	parentIDsFlag := fmt.Sprintf("--parentID=%s", joinedVMBusIDs)
	args := []string{deviceUtilPath, "children", parentIDsFlag, "--property=location"}
	return args
}

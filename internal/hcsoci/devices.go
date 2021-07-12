package hcsoci

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Microsoft/hcsshim/internal/devices"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/oci"
	"github.com/Microsoft/hcsshim/internal/resources"
	"github.com/Microsoft/hcsshim/internal/uvm"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

const deviceUtilExeName = "device-util.exe"

// getAssignedDeviceKernelDrivers gets any device drivers specified on the spec.
// Drivers are optional, therefore do not return an error if none are on the spec.
//
// See comment on oci.AnnotationAssignedDeviceKernelDrivers for expected format.
func getAssignedDeviceKernelDrivers(annotations map[string]string) ([]string, error) {
	csDrivers, ok := annotations[oci.AnnotationAssignedDeviceKernelDrivers]
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

// getDeviceUtilHostPath is a simple helper function to find the host path of the device-util tool
func getDeviceUtilHostPath() string {
	return filepath.Join(filepath.Dir(os.Args[0]), deviceUtilExeName)
}

// handleAssignedDevicesWindows does all of the work to setup the hosting UVM, assign in devices
// specified on the spec, and install any necessary, specified kernel drivers into the UVM.
//
// Drivers must be installed after the target devices are assigned into the UVM.
// This ordering allows us to guarantee that driver installation on a device in the UVM is completed
// before we attempt to create a container.
func handleAssignedDevicesWindows(ctx context.Context, vm *uvm.UtilityVM, annotations map[string]string, specDevs []specs.WindowsDevice) (resultDevs []specs.WindowsDevice, closers []resources.ResourceCloser, err error) {
	defer cleanupClosersOnFailure(ctx, closers, err)

	// install the device util tool in the UVM
	toolHostPath := getDeviceUtilHostPath()
	options := vm.DefaultVSMBOptions(true)
	toolsShare, err := vm.AddVSMB(ctx, toolHostPath, options)
	if err != nil {
		return nil, closers, fmt.Errorf("failed to add VSMB share to utility VM for path %+v: %s", toolHostPath, err)
	}
	closers = append(closers, toolsShare)
	deviceUtilPath, err := vm.GetVSMBUvmPath(ctx, toolHostPath, true)
	if err != nil {
		return nil, closers, err
	}

	// assign device into UVM and create corresponding spec windows devices
	for _, d := range specDevs {
		devID, index := getDeviceInfoFromID(d.ID)
		vpciCloser, locationPaths, err := devices.AddDevice(ctx, vm, d.IDType, devID, index, deviceUtilPath)
		if err != nil {
			return nil, nil, err
		}
		closers = append(closers, vpciCloser)
		for _, value := range locationPaths {
			specDev := specs.WindowsDevice{
				ID:     value,
				IDType: uvm.VPCILocationPathIDType,
			}
			log.G(ctx).WithField("parsed devices", specDev).Info("added windows device to spec")
			resultDevs = append(resultDevs, specDev)
		}
	}

	return resultDevs, closers, nil
}

func handleAssignedDevicesLCOW(ctx context.Context, vm *uvm.UtilityVM, annotations map[string]string, specDevs *[]specs.WindowsDevice, specProcess *specs.Process) (closers []resources.ResourceCloser, err error) {
	defer cleanupClosersOnFailure(ctx, closers, err)

	// TODO katiewasnothere: support non gpu devices as well

	// assign device into UVM and create corresponding spec windows devices
	addedGPUVHD := false
	for i, d := range *specDevs {
		if d.IDType == uvm.VPCIDeviceIDType || d.IDType == uvm.VPCIDeviceIDTypeLegacy || d.IDType == uvm.GPUDeviceIDType {
			devID, index := getDeviceInfoFromID(d.ID)
			vpci, err := devices.AddDeviceLCOWPlain(ctx, vm, devID, index)
			if err != nil && err != devices.NoExecOutputErr {
				return closers, err
			}

			closers = append(closers, vpci)
			// update device ID on the spec to the assigned device's resulting vmbus guid so gcs knows which devices to
			// map into the container
			(*specDevs)[i].ID = vpci.VMBusGUID

			if d.IDType == uvm.GPUDeviceIDType && !addedGPUVHD {
				gpuSupportVhdPath, err := getGPUVHDPath(annotations)
				if err != nil {
					return closers, errors.Wrapf(err, "failed to add gpu vhd to %v", vm.ID())
				}
				// use lcowNvidiaMountPath since we only support nvidia gpus right now
				// must use scsi here since DDA'ing a hyper-v pci device is not supported on VMs that have ANY virtual memory
				// gpuvhd must be granted VM Group access.
				options := []string{"ro"}
				scsiMount, err := vm.AddSCSI(ctx, gpuSupportVhdPath, uvm.LCOWNvidiaMountPath, true, options, uvm.VMAccessTypeNoop)
				if err != nil {
					return closers, errors.Wrapf(err, "failed to add scsi device %s in the UVM %s at %s", gpuSupportVhdPath, vm.ID(), uvm.LCOWNvidiaMountPath)
				}
				closers = append(closers, scsiMount)
				addedGPUVHD = true
			}
		}
	}
	return closers, nil
}

func getDeviceInfoFromID(deviceID string) (string, uint16) {
	indexString := filepath.Base(deviceID)
	index, err := strconv.ParseUint(indexString, 10, 16)
	if err == nil {
		// we have a vf index
		return filepath.Dir(deviceID), uint16(index)
	}
	// otherwise, just use default index and full device ID given
	return deviceID, 0
}

func installPodDrivers(ctx context.Context, vm *uvm.UtilityVM, annotations map[string]string) (closers []resources.ResourceCloser, err error) {
	defer cleanupClosersOnFailure(ctx, closers, err)
	// get the spec specified kernel drivers and install them on the UVM
	drivers, err := getAssignedDeviceKernelDrivers(annotations)
	if err != nil {
		return closers, err
	}
	for _, d := range drivers {
		driverCloser, err := devices.InstallKernelDriver(ctx, vm, d)
		if err != nil {
			return closers, err
		}
		closers = append(closers, driverCloser)
	}
	return closers, err
}

func cleanupClosersOnFailure(ctx context.Context, closers []resources.ResourceCloser, funcErr error) {
	if funcErr != nil {
		// best effort clean up allocated resources on failure
		for _, r := range closers {
			if releaseErr := r.Release(ctx); releaseErr != nil {
				log.G(ctx).WithError(releaseErr).Error("failed to release container resource")
			}
		}
	}
}

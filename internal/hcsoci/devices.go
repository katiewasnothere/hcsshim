package hcsoci

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	defer func() {
		if err != nil {
			// best effort clean up allocated resources on failure
			for _, r := range closers {
				if releaseErr := r.Release(ctx); releaseErr != nil {
					log.G(ctx).WithError(releaseErr).Error("failed to release container resource")
				}
			}
			closers = nil
			resultDevs = nil
		}
	}()

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
		vpciCloser, locationPaths, err := devices.AddDevice(ctx, vm, d.IDType, d.ID, deviceUtilPath)
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

	// get the spec specified kernel drivers and install them on the UVM
	drivers, err := getAssignedDeviceKernelDrivers(annotations)
	if err != nil {
		return nil, closers, err
	}
	for _, d := range drivers {
		driverCloser, err := devices.InstallKernelDriver(ctx, vm, d)
		if err != nil {
			return nil, closers, err
		}
		closers = append(closers, driverCloser)
	}

	return resultDevs, closers, nil
}

func handleAssignedDevicesLCOW(ctx context.Context, vm *uvm.UtilityVM, annotations map[string]string, specDevs *[]specs.WindowsDevice, specProcess *specs.Process) (closers []resources.ResourceCloser, err error) {
	defer func() {
		if err != nil {
			// best effort clean up allocated resources on failure
			for _, r := range closers {
				if releaseErr := r.Release(ctx); releaseErr != nil {
					log.G(ctx).WithError(releaseErr).Error("failed to release container resource")
				}
			}
			closers = nil
		}
	}()

	// install drivers first so when devices are assigned they're setup correctly
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

		// add bin path for drivers to spec process path
		/*driverBinPaths = append(driverBinPaths, uvmPath+"/bin")
		driverBinPaths = append(driverBinPaths, uvmPath+"/usr/bin")
		driverLibPaths = append(driverLibPaths, uvmPath+"/lib")
		driverLibPaths = append(driverLibPaths, uvmPath+"/usr/lib")*/

	}

	// assign device into UVM and create corresponding spec windows devices
	for i, d := range *specDevs {
		if d.IDType == uvm.VPCIDeviceIDType || d.IDType == uvm.VPCIDeviceIDTypeLegacy || d.IDType == uvm.GPUDeviceIDType {
			vpci, err := devices.AddDeviceLCOW(ctx, vm, d.ID)
			if err != nil && err != devices.NoExecOutputErr {
				return closers, err
			}

			closers = append(closers, vpci)
			// update device ID on the spec to the assigned device's resulting vmbus guid so gcs knows which devices to
			// map into the container
			(*specDevs)[i].ID = vpci.VMBusGUID
		}
	}

	/*devices.UpdateEnvVariableWithPaths(&specProcess.Env, "PATH=", driverBinPaths)
	devices.UpdateEnvVariableWithPaths(&specProcess.Env, "LD_LIBRARY_PATH=", driverLibPaths)*/

	return closers, nil
}

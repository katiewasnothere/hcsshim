// +build windows

package devices

import (
	"context"
	"fmt"

	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/shimdiag"
	"github.com/Microsoft/hcsshim/internal/uvm"
	"github.com/pkg/errors"
)

const uvmPnpExePath = "C:\\Windows\\System32\\pnputil.exe"

// createPnPInstallDriverCommand creates a pnputil command to add and install drivers
// present in `driverUVMPath` and all subdirectories.
func createPnPInstallDriverCommand(driverUVMPath string) []string {
	dirFormatted := fmt.Sprintf("%s/*.inf", driverUVMPath)
	args := []string{
		"cmd",
		"/c",
		uvmPnpExePath,
		"/add-driver",
		dirFormatted,
		"/subdirs",
		"/install",
		// 		"&",
	}
	return args
}

// createPnPInstallAllDriverArgs creates a single command to add and install
// a set of driver locations in the UVM
func createPnPInstallAllDriverArgs(driverUVMDirs []string) []string {
	var result []string
	for _, d := range driverUVMDirs {
		driverArgs := createPnPInstallDriverCommand(d)
		result = append(result, driverArgs...)
	}
	return result
}

// execPnPInstallAllDrivers makes the call to exec in the uvm the pnp command
// that installs all drivers previously mounted into the uvm.
func execPnPInstallAllDrivers(ctx context.Context, vm *uvm.UtilityVM, driverDirs []string) error {
	//args := createPnPInstallAllDriverArgs(driverDirs)

	for _, d := range driverDirs {
		driverArgs := createPnPInstallDriverCommand(d)
		req := &shimdiag.ExecProcessRequest{
			Args: driverArgs,
		}
		exitCode, err := shimdiag.ExecInUvm(ctx, vm, req)
		if err != nil {
			return errors.Wrapf(err, "failed to install drivers in uvm with exit code %d", exitCode)
		}
		log.G(ctx).WithField("added drivers", driverDirs).Debug("installed drivers")
	}
	return nil
}

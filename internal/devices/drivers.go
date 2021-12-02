//go:build windows
// +build windows

package devices

import (
	"context"
	"fmt"

	"github.com/Microsoft/hcsshim/internal/cmd"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/resources"
	"github.com/Microsoft/hcsshim/internal/uvm"
	"github.com/pkg/errors"
)

// InstallKernelDriver mounts a specified kernel driver, then installs it in the UVM.
//
// `driver` is a directory path on the host that contains driver files for standard installation.
// For windows this means files for pnp installation (.inf, .cat, .sys, .cert files).
// For linux this means a vhd file that contains the drivers under /lib/modules/`uname -r` for use
// with depmod and modprobe.
//
// Returns a ResourceCloser for the added mount. On failure, the mounted share will be released,
// the returned ResourceCloser will be nil, and an error will be returned.
func InstallKernelDriver(ctx context.Context, vm *uvm.UtilityVM, driver string) (closer resources.ResourceCloser, _ string, err error) {
	defer func() {
		if err != nil && closer != nil {
			// best effort clean up allocated resource on failure
			if releaseErr := closer.Release(ctx); releaseErr != nil {
				log.G(ctx).WithError(releaseErr).Error("failed to release container resource")
			}
			closer = nil
		}
	}()
	if vm.OS() == "windows" {
		options := vm.DefaultVSMBOptions(true)
		closer, err = vm.AddVSMB(ctx, driver, options)
		if err != nil {
			return closer, "", fmt.Errorf("failed to add VSMB share to utility VM for path %+v: %s", driver, err)
		}
		uvmPath, err := vm.GetVSMBUvmPath(ctx, driver, true)
		if err != nil {
			return closer, "", err
		}
		return closer, uvmPath, execPnPInstallDriver(ctx, vm, uvmPath)
	}
	uvmPathForShare := fmt.Sprintf(uvm.LCOWGlobalMountPrefix, vm.UVMMountCounter())
	scsiCloser, err := vm.AddSCSI(ctx, driver, uvmPathForShare, true, false, []string{}, uvm.VMAccessTypeIndividual)
	if err != nil {
		return closer, "", fmt.Errorf("failed to add SCSI disk to utility VM for path %+v: %s", driver, err)
	}
	uvmPathForShare = scsiCloser.UVMPath
	// TODO katiewasnothere: we should not try to install the drivers again if they've already been installed
	return scsiCloser, uvmPathForShare, execModprobeInstallDriver(ctx, vm, uvmPathForShare)
}

func execModprobeInstallDriver(ctx context.Context, vm *uvm.UtilityVM, driverDir string) error {
	p, l, err := cmd.CreateNamedPipeListener()
	if err != nil {
		return err
	}
	defer l.Close()

	var stderrOutput string
	errChan := make(chan error)

	go readAllPipeOutput(l, errChan, &stderrOutput)

	args := []string{
		"/bin/install-drivers",
		driverDir,
	}
	req := &cmd.CmdProcessRequest{
		Args:   args,
		Stderr: p,
	}

	// A call to `ExecInUvm` may fail in the following ways:
	// - The process runs and exits with a non-zero exit code. In this case we need to wait on the output
	//   from stderr so we can log it for debugging.
	// - There's an error trying to run the process. No need to wait for stderr logs.
	// - There's an error copying IO. No need to wait for stderr logs.
	//
	// Since we cannot distinguish between the cases above, we should always wait to read the stderr output.
	exitCode, execErr := cmd.ExecInUvm(ctx, vm, req)

	// wait to finish parsing stdout results
	select {
	case err := <-errChan:
		if err != nil && err != noExecOutputErr {
			return errors.Wrapf(err, "failed to get stderr output from installing driver %s", driverDir)
		}
	case <-ctx.Done():
		return errors.Wrapf(ctx.Err(), "timed out waiting for the console output from installing driver %s", driverDir)
	}

	if execErr != nil {
		return errors.Wrapf(execErr, "failed to install driver %s in uvm with exit code %d: %v", driverDir, exitCode, stderrOutput)
	}

	log.G(ctx).WithField("added drivers", driverDir).Debug("installed drivers")
	return nil
}

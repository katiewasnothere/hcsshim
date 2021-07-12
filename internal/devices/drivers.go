// +build windows

package devices

import (
	"context"
	"fmt"
	"strings"

	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/resources"
	"github.com/Microsoft/hcsshim/internal/uvm"
)

// InstallKernelDriver mounts a specified kernel driver, then installs it in the UVM based on the OS.
//
// `driver` is a directory path on the host that contains driver files for standard installation.
//
// Returns a ResourceCloser for the added driver share. On failure, the share will be released,
// the returned ResourceCloser will be nil, and an error will be returned.
func InstallKernelDriver(ctx context.Context, vm *uvm.UtilityVM, driver string) (closer resources.ResourceCloser, err error) {
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
			return closer, fmt.Errorf("failed to add VSMB share to utility VM for path %+v: %s", driver, err)
		}
		uvmPath, err := vm.GetVSMBUvmPath(ctx, driver, true)
		if err != nil {
			return closer, err
		}
		return closer, execPnPInstallDriver(ctx, vm, uvmPath)
	}
	uvmPathForShare := fmt.Sprintf(uvm.LCOWGlobalMountPrefix, vm.UVMMountCounter())
	scsiCloser, err := vm.AddSCSI(ctx, driver, uvmPathForShare, false, []string{}, uvm.VMAccessTypeIndividual)
	if err != nil {
		return closer, fmt.Errorf("failed to add SCSI disk to utility VM for path %+v: %s", driver, err)
	}
	return scsiCloser, execModprobeInstallDriver(ctx, vm, uvmPathForShare)
}

func UpdateEnvVariableWithPaths(env *[]string, prefix string, paths []string) {
	pathsJoined := strings.Join(paths, ":")
	for i, v := range *env {
		if strings.HasPrefix(v, prefix) {
			(*env)[i] = fmt.Sprintf("%s:%s", v, pathsJoined)
			return
		}
	}

	// not found, make an entry
	*env = append(*env, prefix+pathsJoined)
}

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Microsoft/hcsshim/internal/cow"
	"github.com/Microsoft/hcsshim/internal/gcs"
	"github.com/Microsoft/hcsshim/internal/shimdiag"
	"github.com/Microsoft/hcsshim/internal/uvm"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

const jobObjUtilExeName = "jobobj-util.exe"
const jobObjUtilUVMPath = "C:\\" + jobObjUtilExeName

func verifyUpdateResourcesType(data interface{}) error {
	switch data.(type) {
	case *specs.WindowsResources:
	case *specs.LinuxResources:
	default:
		return errors.New("update resources must be of type *WindowsResources or *LinuxResources")
	}
	return nil
}

func createJobObjectToolSetCommand(jobObjToolPath, containerID string) []string {
	args := []string{
		"cmd",
		"/c",
		jobObjToolPath,
		"set-jobobj",
		"-containerID",
		containerID,
	}
	return args
}

// getJobObjectUtilHostPath is a simple helper function to find the host path of the jobobj-util tool
func getJobObjectUtilHostPath() string {
	return filepath.Join(filepath.Dir(os.Args[0]), jobObjUtilExeName)
}

func updateWCOWContainerCPU(ctx context.Context, task shimTask, cpuResources *specs.WindowsCPUResources) error {
	// make sure our utility tool is present in the host
	shareReq := &shimdiag.ShareRequest{
		HostPath: getJobObjectUtilHostPath(),
		UvmPath:  jobObjUtilUVMPath,
		ReadOnly: true,
	}
	if err := task.Share(ctx, shareReq); err != nil {
		return err
	}

	// TODO katiewasnothere: fix this? or let it fail?
	args := createJobObjectToolSetCommand(jobObjUtilUVMPath, task.ID())
	if cpuResources.Count != nil && (cpuResources.Shares == nil && cpuResources.Maximum == nil) {
		procCount := *cpuResources.Count
		/*hostCount := ht.host.ProcessorCount()
		if procCount > uint64(hostCount) {
			log.G(ctx).WithFields(logrus.Fields{
				logfields.UVMID: ht.host.ID(),
				"requested":     procCount,
				"assigned":      hostCount,
			}).Warn("Changing user requested CPUCount to current number of processors")
			procCount = uint64(hostCount)
		}*/
		args = append(args, "-processorCount", strconv.Itoa(int(procCount)))
	} else if cpuResources.Shares != nil && (cpuResources.Count == nil && cpuResources.Maximum == nil) {
		args = append(args, "-processorWeight", strconv.Itoa(int(*cpuResources.Shares)))
	} else if cpuResources.Maximum != nil && (cpuResources.Count == nil && cpuResources.Shares == nil) {
		args = append(args, "-processorLimit", strconv.Itoa(int(*cpuResources.Maximum)))
	} else {
		return fmt.Errorf("invalid cpu resources request for container %s: %v", task.ID(), cpuResources)
	}

	req := &shimdiag.ExecProcessRequest{
		Args: args,
	}
	if exitCode, err := task.ExecInHost(ctx, req); err != nil {
		return errors.Wrapf(err, "failed to exec command %v in host: %d", args, exitCode)
	}

	return nil
}

func updateWCOWResources(ctx context.Context, task shimTask, c cow.Container, data interface{}) error {
	resources, ok := data.(*specs.WindowsResources)
	if !ok {
		return errors.New("must have resources be type *WindowsResources when updating a wcow container")
	}
	if resources.Memory != nil && resources.Memory.Limit != nil {
		if err := gcs.UpdateContainerMemory(ctx, c, *resources.Memory.Limit); err != nil {
			return err
		}
	}
	if resources.CPU != nil {
		if err := updateWCOWContainerCPU(ctx, task, resources.CPU); err != nil {
			return err
		}
	}
	return nil
}

func updateLCOWResources(ctx context.Context, vm *uvm.UtilityVM, id string, data interface{}) error {
	resources, ok := data.(*specs.LinuxResources)
	if !ok {
		if err := vm.UpdateContainer(ctx, id, resources); err != nil {
			return err
		}
	}
	return nil
}

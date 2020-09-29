package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"unsafe"

	"github.com/Microsoft/hcsshim/internal/uvm"

	"github.com/Microsoft/hcsshim/internal/winapi"
	"golang.org/x/sys/windows"
)

const (
	jobObjectQuery     = 0x0004
	jobObjectAllAccess = 0x1F001F

	processorWeightMax float64 = 10000

	memoryLimitMax uint64 = 0xffffffffffffffff
)

func getWindowsContainerJobName(containerID string) string {
	return "\\Container_" + containerID
}

// getWindowsContainerJobHandleAllAccess opens the job object of a container
func getWindowsContainerJobHandleAllAccess(containerID string) (windows.Handle, error) {
	// TODO katiewasnothere: get the type of container to see iff windows
	jobName := getWindowsContainerJobName(containerID)
	fmt.Fprintln(os.Stdout, jobName)
	unicodeJobName, err := winapi.NewUnicodeString(jobName)
	if err != nil {
		return 0, err
	}

	var oa winapi.ObjectAttributes
	// TODO katiewasnothere: figure out issue with this call
	// winapi.InitializeObjectAttributes(&oa, unicodeJobName, 0, 0, nil)
	oa.Length = unsafe.Sizeof(winapi.ObjectAttributes{})
	oa.ObjectName = uintptr(unsafe.Pointer(unicodeJobName))
	oa.Attributes = 0

	var jobHandle windows.Handle
	status := winapi.NtOpenJobObject(&jobHandle, jobObjectAllAccess, &oa)

	if status != 0 {
		return 0, winapi.RtlNtStatusToDosError(status)
	}
	return jobHandle, nil
}

// todo katiewasnothere: if this stays, we need a lock
func getJobObjectCpuInformation(containerID string) (*winapi.JOBOBJECT_CPU_RATE_CONTROL_INFORMATION, error) {
	jobHandle, err := getWindowsContainerJobHandleAllAccess(containerID)
	if err != nil {
		return nil, err
	}

	info := winapi.JOBOBJECT_CPU_RATE_CONTROL_INFORMATION{}
	if err = winapi.QueryInformationJobObject(
		jobHandle,
		windows.JobObjectCpuRateControlInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
		nil,
	); err != nil {
		return nil, err
	}
	return &info, nil
}

func getJobObjectExtendedInfo(containerID string) (*windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION, error) {
	jobHandle, err := getWindowsContainerJobHandleAllAccess(containerID)
	if err != nil {
		return nil, err
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	if err = winapi.QueryInformationJobObject(
		jobHandle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
		nil,
	); err != nil {
		return nil, err
	}
	return &info, nil
}

func GetJobObjectCPURate(containerID string) (uint32, error) {
	info, err := getJobObjectCpuInformation(containerID)
	if err != nil {
		return 0, err
	}
	if !isFlagSet(winapi.JOB_OBJECT_CPU_RATE_CONTROL_ENABLE, info.ControlFlags) {
		return 0, fmt.Errorf("the job for container %s does not have cpu rate control enabled", containerID)
	}
	if !isFlagSet(winapi.JOB_OBJECT_CPU_RATE_CONTROL_HARD_CAP, info.ControlFlags) {
		return 0, fmt.Errorf("the job for container %s does not have cpu rate hard cap option set", containerID)
	}
	return info.Value, nil
}

func GetJobObjectCPUWeight(containerID string) (uint32, error) {
	info, err := getJobObjectCpuInformation(containerID)
	if err != nil {
		return 0, err
	}

	if !isFlagSet(winapi.JOB_OBJECT_CPU_RATE_CONTROL_ENABLE, info.ControlFlags) {
		return 0, fmt.Errorf("the job for container %s does not have cpu rate control enabled", containerID)
	}
	if !isFlagSet(winapi.JOB_OBJECT_CPU_RATE_CONTROL_WEIGHT_BASED, info.ControlFlags) {
		return 0, fmt.Errorf("the job for container %s does not have cpu weight option set", containerID)
	}
	return info.Value, nil
}

func setJobObjectCPUResources(containerID string, controlFlags, value uint32) error {
	jobHandle, err := getWindowsContainerJobHandleAllAccess(containerID)
	if err != nil {
		return err
	}

	cpuInfo := winapi.JOBOBJECT_CPU_RATE_CONTROL_INFORMATION{
		ControlFlags: controlFlags,
		Value:        value,
	}

	if _, err := windows.SetInformationJobObject(
		jobHandle,
		windows.JobObjectCpuRateControlInformation,
		uintptr(unsafe.Pointer(&cpuInfo)),
		uint32(unsafe.Sizeof(cpuInfo)),
	); err != nil {
		return err
	}
	return nil
}

func SetJobObjectCPURate(containerID string, cpuRate uint32) error {
	controlFlags := winapi.JOB_OBJECT_CPU_RATE_CONTROL_ENABLE | winapi.JOB_OBJECT_CPU_RATE_CONTROL_HARD_CAP
	return setJobObjectCPUResources(containerID, controlFlags, cpuRate)
}

func SetJobObjectCPUWeight(containerID string, cpuWeight uint32) error {
	controlFlags := winapi.JOB_OBJECT_CPU_RATE_CONTROL_ENABLE | winapi.JOB_OBJECT_CPU_RATE_CONTROL_WEIGHT_BASED
	return setJobObjectCPUResources(containerID, controlFlags, cpuWeight)
}

func isFlagSet(flag uint32, controlFlags uint32) bool {
	if (flag & controlFlags) == flag {
		return true
	}
	return false
}

func CalculateJobCPURate(ctx context.Context, processorCount uint32) (uint32, error) {
	hostTopology, err := uvm.HostProcessorInfo(ctx)
	if err != nil {
		return 0, err
	}

	hostProcs := hostTopology.LogicalProcessorCount
	if processorCount > hostProcs {
		return 0, fmt.Errorf("requested %d processors, but the host only has %d", processorCount, hostProcs)
	}

	rate := (processorCount * 10000) / hostProcs
	if rate == 0 {
		return 1, nil
	}

	return rate, nil
}

func CalculateProcessorCount(ctx context.Context, jobCPURate uint32) (uint32, error) {
	hostTopology, err := uvm.HostProcessorInfo(ctx)
	if err != nil {
		return 0, err
	}

	hostProcs := hostTopology.LogicalProcessorCount
	processorCount := (jobCPURate * hostProcs) / 10000
	return processorCount, nil
}

func CalculateJobCPUWeight(processorWeight uint32) uint32 {
	jobWeight := 1 + uint32((8*float64(processorWeight))/processorWeightMax)
	return jobWeight
}

func CalculateProcessorWeight(jobCPUWeight uint32) (uint32, error) {
	if jobCPUWeight == 0 {
		return 0, errors.New("job CPU weight cannot be 0 to convert to processor weight")
	}
	processorWeight := uint32((processorWeightMax / 8) * (float64(jobCPUWeight) - 1))
	return processorWeight, nil
}

func setJobObjExtendedInfo(containerID string, extendedInfo *windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION) error {
	jobHandle, err := getWindowsContainerJobHandleAllAccess(containerID)
	if err != nil {
		return err
	}

	if _, err := windows.SetInformationJobObject(
		jobHandle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(extendedInfo)),
		uint32(unsafe.Sizeof(*extendedInfo)),
	); err != nil {
		return err
	}
	return nil
}

func SetJobObjMemoryLimit(containerID string, memoryLimit uint64) error {
	extendedInfo := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		JobMemoryLimit: uintptr(memoryLimit),
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: uint32(windows.JOB_OBJECT_LIMIT_JOB_MEMORY),
		},
	}
	return setJobObjExtendedInfo(containerID, &extendedInfo)
}

func SetJobObjectAffinity(containerID string, bitmask uint64) error {
	extendedInfo := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: uint32(windows.JOB_OBJECT_LIMIT_AFFINITY),
			Affinity:   uintptr(bitmask),
		},
	}
	return setJobObjExtendedInfo(containerID, &extendedInfo)
}

func CalculateJobMemoryLimit(memoryLimitInMB uint64) (uint64, error) {
	jobObjMemLimit := memoryLimitInMB << 20
	if !(jobObjMemLimit < memoryLimitMax) {
		return 0, errors.New("memory limit specified exceeds the max size")
	}
	return jobObjMemLimit, nil
}

func CalculateMemoryInMB(jobObjMemoryLimit uint64) uint64 {
	return jobObjMemoryLimit >> 20
}

func GetJobObjectMemoryLimit(containerID string) (uint64, error) {
	extendedInfo, err := getJobObjectExtendedInfo(containerID)
	if err != nil {
		return 0, err
	}
	return uint64(extendedInfo.JobMemoryLimit), nil
}

func GetJobObjectAffinity(containerID string) (uint64, error) {
	extendedInfo, err := getJobObjectExtendedInfo(containerID)
	if err != nil {
		return 0, err
	}
	return uint64(extendedInfo.BasicLimitInformation.Affinity), nil
}

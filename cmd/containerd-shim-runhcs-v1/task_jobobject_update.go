package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Microsoft/hcsshim/internal/jobobject"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/processorinfo"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
)

const (
	jobObjUtilExeName = "jobobj-util.exe"

	cpuLimitFlag     = "cpu-limit"
	cpuWeightFlag    = "cpu-weight"
	useNTVariantFlag = "use-nt"
)

func calculateJobCPURate(ctx context.Context, requestCount uint32) uint32 {
	hostProcs := uint32(processorinfo.ProcessorCount())
	if requestCount > hostProcs {
		log.G(ctx).WithFields(logrus.Fields{
			"requested": requestCount,
			"assigned":  hostProcs,
		}).Warn("Changing user requested CPUCount to current number of processors")
		requestCount = hostProcs
	}

	rate := (requestCount * 10000) / hostProcs
	if rate == 0 {
		return 1
	}
	return rate
}

func calculateJobCPUWeight(processorWeight uint32) uint32 {
	return 1 + uint32((8*processorWeight)/jobobject.CPUWeightMax)
}

func createJobObjectsUtilArgs(ctx context.Context, toolPath string, procResources *specs.WindowsCPUResources) []string {
	args := []string{"cmd", "/c", toolPath, "set", useNTVariantFlag}
	if procResources.Count != nil {
		procCount := *procResources.Count
		jobCPURate := calculateJobCPURate(ctx, uint32(procCount))
		args = append(args, cpuLimitFlag, strconv.Itoa(int(jobCPURate)))
	} else if procResources.Shares != nil {
		procWeight := *procResources.Shares
		jobWeight := calculateJobCPUWeight(uint32(procWeight))
		args = append(args, cpuWeightFlag, strconv.Itoa(int(jobWeight)))
	} else if procResources.Maximum != nil {
		jobMax := *procResources.Maximum
		args = append(args, cpuLimitFlag, strconv.Itoa(int(jobMax)))
	}
	return args
}

func getToolPaths() (string, string) {
	hostPath := filepath.Join(filepath.Dir(os.Args[0]), jobObjUtilExeName)
	guestPath := "C:\\" + jobObjUtilExeName
	return hostPath, guestPath
}

package oci

import (
	"context"

	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/uvm"
	"github.com/Microsoft/hcsshim/osversion"
)

// HandleCPUGroupSetup will parse the cpugroup annotations and setup the cpugroup for `vm`
func HandleCPUGroupSetup(ctx context.Context, vm *uvm.UtilityVM, annotations map[string]string) error {
	// TODO katiewasnothere: this may not be entirely accurate
	if osversion.Get().Build < 20196 {
		return nil
	}
	cpuGroupOpts := AnnotationsToCPUGroupOptions(ctx, annotations)
	if cpuGroupOpts.ID == "" {
		// user did not set any cpugroup requests, skip setting anything up
		return nil
	}
	log.G(ctx).WithField("opts", cpuGroupOpts).Info("Parsed annotations for cpugroup options")
	if err := vm.ConfigureVMCPUGroup(ctx, cpuGroupOpts); err != nil {
		return err
	}
	return nil
}

// AnnotationsToCPUGroupOptions parses the related cpugroup annotations and creates the CPUGroupOptions from the values
func AnnotationsToCPUGroupOptions(ctx context.Context, annotations map[string]string) *uvm.CPUGroupOptions {
	id := parseAnnotationsString(annotations, annotationCPUGroupID, "")

	opts := &uvm.CPUGroupOptions{
		ID: id,
	}

	/*if cap, ok := annotations[annotationCPUGroupCap]; ok {
		countu, err := strconv.ParseUint(cap, 10, 32)
		if err == nil {
			v := uint32(countu)
			opts.Cap = &v
		}
	}
	if pri, ok := annotations[annotationCPUGroupPriority]; ok {
		countu, err := strconv.ParseUint(pri, 10, 32)
		if err == nil {
			v := uint32(countu)
			opts.Priority = &v
		}
	}*/
	return opts
}

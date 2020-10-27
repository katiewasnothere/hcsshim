package oci

import (
	"context"

	"github.com/Microsoft/hcsshim/internal/uvm"
)

// HandleCPUGroupSetup will parse the cpugroupID annotation and setup the cpugroup for `vm`
func HandleCPUGroupSetup(ctx context.Context, vm *uvm.UtilityVM, annotations map[string]string) error {
	id := parseAnnotationsString(annotations, annotationCPUGroupID, "")
	if id == "" {
		// user did not set any cpugroup ID request, skip
		return nil
	}
	if err := vm.ConfigureVMCPUGroup(ctx, id); err != nil {
		return err
	}
	return nil
}

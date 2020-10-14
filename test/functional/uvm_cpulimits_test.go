package functional

import (
	"context"
	"os"
	"testing"
	"time"

	hcsschema "github.com/Microsoft/hcsshim/internal/schema2"
	"github.com/Microsoft/hcsshim/internal/uvm"
	"github.com/Microsoft/hcsshim/osversion"
	testutilities "github.com/Microsoft/hcsshim/test/functional/utilities"
)

func TestUVMCPULimitsUpdateLCOW(t *testing.T) {
	testutilities.RequiresBuild(t, osversion.RS5)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	opts := uvm.NewDefaultOptionsLCOW(t.Name(), "")
	opts.MemorySizeInMB = 1024 * 2
	u := testutilities.CreateLCOWUVMFromOpts(ctx, t, opts)
	defer u.Close()

	limits := &hcsschema.ProcessorLimits{
		Weight: 10000,
	}
	if err := u.UpdateCPULimits(ctx, limits); err != nil {
		t.Fatalf("failed to update the cpu limits of the UVM with: %v", err)
	}
}

func TestUVMCPULimitsUpdateWCOW(t *testing.T) {
	testutilities.RequiresBuild(t, osversion.RS5)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	opts := uvm.NewDefaultOptionsWCOW(t.Name(), "")
	opts.MemorySizeInMB = 1024 * 2

	u, _, uvmScratchDir := testutilities.CreateWCOWUVMFromOptsWithImage(ctx, t, opts, "mcr.microsoft.com/windows/nanoserver:1909")
	defer os.RemoveAll(uvmScratchDir)
	defer u.Close()

	limits := &hcsschema.ProcessorLimits{
		Weight: 10000,
	}
	if err := u.UpdateCPULimits(ctx, limits); err != nil {
		t.Fatalf("failed to update the cpu limits of the UVM with: %v", err)
	}
}

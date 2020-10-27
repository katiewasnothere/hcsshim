package uvm

import (
	"context"
	"testing"

	"github.com/Microsoft/go-winio/pkg/guid"
)

// Unit tests for cpugroup creation, modification, and deletion
func DisabledTestCPUGroupCreateWithIDAndDelete(t *testing.T) {
	lps := []uint32{0, 1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id, err := guid.NewV4()
	if err != nil {
		t.Fatalf("failed to create cpugroup guid with: %v", err)
	}
	err = CreateNewCPUGroupWithID(ctx, id.String(), lps)
	if err != nil {
		t.Fatalf("failed to create cpugroup %s with: %v", id.String(), err)
	}
	defer func() {
		if err := deleteCPUGroup(ctx, id.String()); err != nil {
			t.Fatalf("failed to delete cpugroup %s with: %v", id.String(), err)
		}
	}()

	exists, err := cpuGroupExists(ctx, id.String())
	if err != nil {
		t.Fatalf("failed to determine if cpugroup exists with: %v", err)
	}
	if !exists {
		t.Fatalf("expected to find cpugroup %s on machine but didn't", id.String())
	}
}

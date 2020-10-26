// +build functional

package cri_containerd

import (
	"context"
	"fmt"
	"testing"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/containerd/cri/criextension"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

func Test_Pod_UpdateResources_Memory(t *testing.T) {
	type config struct {
		name             string
		requiredFeatures []string
		runtimeHandler   string
		sandboxImage     string
	}
	tests := []config{
		{
			name:             "WCOW_Hypervisor",
			requiredFeatures: []string{featureWCOWHypervisor},
			runtimeHandler:   wcowHypervisorRuntimeHandler,
			sandboxImage:     imageWindowsNanoserver,
		},
		{
			name:             "LCOW",
			requiredFeatures: []string{featureLCOW},
			runtimeHandler:   lcowRuntimeHandler,
			sandboxImage:     imageLcowK8sPause,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireFeatures(t, test.requiredFeatures...)

			if test.runtimeHandler == lcowRuntimeHandler {
				pullRequiredLcowImages(t, []string{test.sandboxImage})
			} else {
				pullRequiredImages(t, []string{test.sandboxImage})
			}
			var startingMemorySize int64 = 768 * 1024 * 1024
			podRequest := &runtime.RunPodSandboxRequest{
				Config: &runtime.PodSandboxConfig{
					Metadata: &runtime.PodSandboxMetadata{
						Name:      t.Name(),
						Uid:       "0",
						Namespace: testNamespace,
					},
					Annotations: map[string]string{
						"io.microsoft.container.memory.sizeinmb": fmt.Sprintf("%d", startingMemorySize), // 768MB
					},
				},
				RuntimeHandler: test.runtimeHandler,
			}

			client := newTestRuntimeClient(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			podID := runPodSandbox(t, client, ctx, podRequest)
			defer removePodSandbox(t, client, ctx, podID)
			defer stopPodSandbox(t, client, ctx, podID)

			// make request for shrinking memory size
			criExtensionClient := newTestCRIExtensionClient(t)
			newMemorySize := startingMemorySize / 2
			updateReq := &criextension.UpdateContainerResourcesV2Request{
				ContainerId: podID,
			}

			if test.runtimeHandler == lcowRuntimeHandler {
				updateReq.StdLinuxResources = &runtime.LinuxContainerResources{
					MemoryLimitInBytes: newMemorySize,
				}
			} else {
				updateReq.StdWindowsResources = &runtime.WindowsContainerResources{
					MemoryLimitInBytes: newMemorySize,
				}
			}

			if _, err := criExtensionClient.UpdateContainerResourcesV2(ctx, updateReq); err != nil {
				t.Fatalf("updating container resources for %s with %v", podID, err)
			}

		})
	}
}

func Test_Pod_UpdateResources_Memory_PA(t *testing.T) {
	type config struct {
		name             string
		requiredFeatures []string
		runtimeHandler   string
		sandboxImage     string
	}
	tests := []config{
		{
			name:             "WCOW_Hypervisor",
			requiredFeatures: []string{featureWCOWHypervisor},
			runtimeHandler:   wcowHypervisorRuntimeHandler,
			sandboxImage:     imageWindowsNanoserver,
		},
		{
			name:             "LCOW",
			requiredFeatures: []string{featureLCOW},
			runtimeHandler:   lcowRuntimeHandler,
			sandboxImage:     imageLcowK8sPause,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireFeatures(t, test.requiredFeatures...)

			if test.runtimeHandler == lcowRuntimeHandler {
				pullRequiredLcowImages(t, []string{test.sandboxImage})
			} else {
				pullRequiredImages(t, []string{test.sandboxImage})
			}
			var startingMemorySize int64 = 200 * 1024 * 1024
			podRequest := &runtime.RunPodSandboxRequest{
				Config: &runtime.PodSandboxConfig{
					Metadata: &runtime.PodSandboxMetadata{
						Name:      t.Name(),
						Uid:       "0",
						Namespace: testNamespace,
					},
					Annotations: map[string]string{
						"io.microsoft.virtualmachine.fullyphysicallybacked": "true",
						"io.microsoft.container.memory.sizeinmb":            fmt.Sprintf("%d", startingMemorySize), // 768MB
					},
				},
				RuntimeHandler: test.runtimeHandler,
			}

			client := newTestRuntimeClient(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			podID := runPodSandbox(t, client, ctx, podRequest)
			defer removePodSandbox(t, client, ctx, podID)
			defer stopPodSandbox(t, client, ctx, podID)

			// make request for shrinking memory size
			criExtensionClient := newTestCRIExtensionClient(t)
			newMemorySize := startingMemorySize / 2
			updateReq := &criextension.UpdateContainerResourcesV2Request{
				ContainerId: podID,
			}

			if test.runtimeHandler == lcowRuntimeHandler {
				updateReq.StdLinuxResources = &runtime.LinuxContainerResources{
					MemoryLimitInBytes: newMemorySize,
				}
			} else {
				updateReq.StdWindowsResources = &runtime.WindowsContainerResources{
					MemoryLimitInBytes: newMemorySize,
				}
			}

			if _, err := criExtensionClient.UpdateContainerResourcesV2(ctx, updateReq); err != nil {
				t.Fatalf("updating container resources for %s with %v", podID, err)
			}

		})
	}
}

const (
	annotationCPUGroupID  = "io.microsoft.virtualmachine.cpugroup.id"
	annotationCPUGroupCap = "io.microsoft.virtualmachine.cpugroup.cap"
)

func Test_Pod_UpdateResources_CPUGroup(t *testing.T) {
	type config struct {
		name             string
		requiredFeatures []string
		runtimeHandler   string
		sandboxImage     string
	}
	tests := []config{
		{
			name:             "WCOW_Hypervisor",
			requiredFeatures: []string{featureWCOWHypervisor},
			runtimeHandler:   wcowHypervisorRuntimeHandler,
			sandboxImage:     imageWindowsNanoserver,
		},
		{
			name:             "LCOW",
			requiredFeatures: []string{featureLCOW},
			runtimeHandler:   lcowRuntimeHandler,
			sandboxImage:     imageLcowK8sPause,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireFeatures(t, test.requiredFeatures...)

			if test.runtimeHandler == lcowRuntimeHandler {
				pullRequiredLcowImages(t, []string{test.sandboxImage})
			} else {
				pullRequiredImages(t, []string{test.sandboxImage})
			}
			podRequest := &runtime.RunPodSandboxRequest{
				Config: &runtime.PodSandboxConfig{
					Metadata: &runtime.PodSandboxMetadata{
						Name:      t.Name(),
						Uid:       "0",
						Namespace: testNamespace,
					},
				},
				RuntimeHandler: test.runtimeHandler,
			}

			client := newTestRuntimeClient(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			podID := runPodSandbox(t, client, ctx, podRequest)
			defer removePodSandbox(t, client, ctx, podID)
			defer stopPodSandbox(t, client, ctx, podID)

			// make request for updating cpugroup
			criExtensionClient := newTestCRIExtensionClient(t)
			updateReq := &criextension.UpdateContainerResourcesV2Request{
				ContainerId: podID,
				Annotations: map[string]string{},
			}

			id, err := guid.NewV4()
			if err != nil {
				t.Fatalf("failed to get cpugroup guid with: %v", err)
			}
			updateReq.Annotations[annotationCPUGroupID] = id.String()
			updateReq.Annotations[annotationCPUGroupCap] = "2000"

			if _, err := criExtensionClient.UpdateContainerResourcesV2(ctx, updateReq); err != nil {
				t.Fatalf("updating container resources for %s with %v", podID, err)
			}

		})
	}
}

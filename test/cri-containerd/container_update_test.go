// +build functional

package cri_containerd

import (
	"context"
	"fmt"
	"testing"

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

type config struct {
	name             string
	requiredFeatures []string
	runtimeHandler   string
	sandboxImage     string
	containerImage   string
	cmd              []string
}

func Test_Container_UpdateResources_CPUShare(t *testing.T) {
	tests := []config{
		{
			name:             "WCOW_Process",
			requiredFeatures: []string{featureWCOWProcess},
			runtimeHandler:   wcowProcessRuntimeHandler,
			sandboxImage:     imageWindowsNanoserverCustom,
			containerImage:   imageWindowsNanoserverCustom,
			cmd:              []string{"cmd", "/c", "ping", "-t", "127.0.0.1"},
		},
		{
			name:             "WCOW_Hypervisor",
			requiredFeatures: []string{featureWCOWHypervisor},
			runtimeHandler:   wcowHypervisorRuntimeHandler,
			sandboxImage:     imageWindowsNanoserver,
			containerImage:   imageWindowsNanoserver,
			cmd:              []string{"cmd", "/c", "ping", "-t", "127.0.0.1"},
		},
		{
			name:             "LCOW",
			requiredFeatures: []string{featureLCOW},
			runtimeHandler:   lcowRuntimeHandler,
			sandboxImage:     imageLcowK8sPause,
			containerImage:   imageLcowAlpine,
			cmd:              []string{"top"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireFeatures(t, test.requiredFeatures...)

			if test.runtimeHandler == lcowRuntimeHandler {
				pullRequiredLcowImages(t, []string{test.sandboxImage})
			} else if test.runtimeHandler == wcowHypervisorRuntimeHandler {
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

			containerRequest := &runtime.CreateContainerRequest{
				Config: &runtime.ContainerConfig{
					Metadata: &runtime.ContainerMetadata{
						Name: t.Name() + "-Container",
					},
					Image: &runtime.ImageSpec{
						Image: test.containerImage,
					},
					Command: test.cmd,
				},
				PodSandboxId:  podID,
				SandboxConfig: podRequest.Config,
			}

			// TODO katiewasnothere: does this clean up as expected?
			containerID := createContainer(t, client, ctx, containerRequest)
			defer removeContainer(t, client, ctx, containerID)

			startContainer(t, client, ctx, containerID)
			defer stopContainer(t, client, ctx, containerID)

			// make request to increase cpu shares == cpu weight
			updateReq := &runtime.UpdateContainerResourcesRequest{
				ContainerId: podID,
			}

			if test.runtimeHandler == lcowRuntimeHandler {
				updateReq.Linux = &runtime.LinuxContainerResources{
					CpuShares: 5000,
				}
			} else {
				updateReq.Windows = &runtime.WindowsContainerResources{
					CpuShares: 5000,
				}
			}

			if _, err := client.UpdateContainerResources(ctx, updateReq); err != nil {
				t.Fatalf("updating container resources for %s with %v", containerID, err)
			}

			// TODO katiewasnothere: verify results
		})
	}
}

func Test_Container_UpdateResources_Memory(t *testing.T) {
	tests := []config{
		{
			name:             "WCOW_Process",
			requiredFeatures: []string{featureWCOWProcess},
			runtimeHandler:   wcowProcessRuntimeHandler,
			sandboxImage:     imageWindowsNanoserverCustom,
			containerImage:   imageWindowsNanoserverCustom,
			cmd:              []string{"cmd", "/c", "ping", "-t", "127.0.0.1"},
		},
		{
			name:             "WCOW_Hypervisor",
			requiredFeatures: []string{featureWCOWHypervisor},
			runtimeHandler:   wcowHypervisorRuntimeHandler,
			sandboxImage:     imageWindowsNanoserver,
			containerImage:   imageWindowsNanoserver,
			cmd:              []string{"cmd", "/c", "ping", "-t", "127.0.0.1"},
		},
		{
			name:             "LCOW",
			requiredFeatures: []string{featureLCOW},
			runtimeHandler:   lcowRuntimeHandler,
			sandboxImage:     imageLcowK8sPause,
			containerImage:   imageLcowAlpine,
			cmd:              []string{"top"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireFeatures(t, test.requiredFeatures...)

			if test.runtimeHandler == lcowRuntimeHandler {
				pullRequiredLcowImages(t, []string{test.sandboxImage})
			} else if test.runtimeHandler == wcowHypervisorRuntimeHandler {
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

			var startingMemorySize int64 = 768 * 1024 * 1024
			containerRequest := &runtime.CreateContainerRequest{
				Config: &runtime.ContainerConfig{
					Metadata: &runtime.ContainerMetadata{
						Name: t.Name() + "-Container",
					},
					Image: &runtime.ImageSpec{
						Image: test.containerImage,
					},
					Command: test.cmd,
					Annotations: map[string]string{
						"io.microsoft.container.memory.sizeinmb": fmt.Sprintf("%d", startingMemorySize), // 768MB
					},
				},
				PodSandboxId:  podID,
				SandboxConfig: podRequest.Config,
			}

			// TODO katiewasnothere: does this clean up as expected?
			containerID := createContainer(t, client, ctx, containerRequest)
			defer removeContainer(t, client, ctx, containerID)

			startContainer(t, client, ctx, containerID)
			defer stopContainer(t, client, ctx, containerID)

			// make request for cpu shares
			updateReq := &runtime.UpdateContainerResourcesRequest{
				ContainerId: podID,
			}

			newMemorySize := startingMemorySize / 2
			if test.runtimeHandler == lcowRuntimeHandler {
				updateReq.Linux = &runtime.LinuxContainerResources{
					MemoryLimitInBytes: newMemorySize,
				}
			} else {
				updateReq.Windows = &runtime.WindowsContainerResources{
					MemoryLimitInBytes: newMemorySize,
				}
			}

			if _, err := client.UpdateContainerResources(ctx, updateReq); err != nil {
				t.Fatalf("updating container resources for %s with %v", containerID, err)
			}

			// TODO katiewasnothere: verify results
		})
	}
}

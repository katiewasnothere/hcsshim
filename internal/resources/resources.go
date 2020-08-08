package resources

import (
	"context"
	"errors"
	"os"

	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/ospath"
	"github.com/Microsoft/hcsshim/internal/uvm"
)

const (
	ScratchPath = "scratch"
	RootfsPath  = "rootfs"
)

// GetNetNS returns the network namespace for the container
func (r *Resources) GetNetNS() string {
	return r.NetNS
}

// Resources is the structure returned as part of creating a container. It holds
// nothing useful to clients, hence everything is lowercased. A client would use
// it in a call to ReleaseResources to ensure everything is cleaned up when a
// container exits.
type Resources struct {
	ID string
	// containerRootInUVM is the base path in a utility VM where elements relating
	// to a container are exposed. For example, the mounted filesystem; the runtime
	// spec (in the case of LCOW); overlay and scratch (in the case of LCOW).
	//
	// For WCOW, this will be under wcowRootInUVM. For LCOW, this will be under
	// lcowRootInUVM, this will also be the "OCI Bundle Path".
	ContainerRootInUVM string
	NetNS              string
	// createNetNS indicates if the network namespace has been created
	CreatedNetNS bool
	// addedNetNSToVM indicates if the network namespace has been added to the containers utility VM
	AddedNetNSToVM bool
	// layers is a pointer to a struct of the layers paths of a container
	Layers *ImageLayers
	// resources is an array of the resources associated with a container
	Resources []ResourceCloser
}

// ResourceCloser is a generic interface for the releasing of a resource. If a resource implements
// this interface(which they all should), freeing of that resource should entail one call to
// <resourceName>.Release(ctx)
type ResourceCloser interface {
	Release(context.Context) error
}

// AutoManagedVHD struct representing a VHD that will be cleaned up automatically.
type AutoManagedVHD struct {
	HostPath string
}

// Release removes the vhd.
func (vhd *AutoManagedVHD) Release(ctx context.Context) error {
	if err := os.Remove(vhd.HostPath); err != nil {
		log.G(ctx).WithField("hostPath", vhd.HostPath).WithError(err).Error("failed to remove automanage-virtual-disk")
	}
	return nil
}

// ReleaseResources releases/frees all of the resources associated with a container. This includes
// Plan9 shares, vsmb mounts, pipe mounts, network endpoints, scsi mounts, vpci devices and layers.
// TODO: make method on Resources struct.
func ReleaseResources(ctx context.Context, r *Resources, vm *uvm.UtilityVM, all bool) error {
	if vm != nil {
		if r.AddedNetNSToVM {
			if err := vm.RemoveNetNS(ctx, r.NetNS); err != nil {
				log.G(ctx).Warn(err)
			}
			r.AddedNetNSToVM = false
		}
	}

	releaseErr := false
	// Release resources in reverse order so that the most recently
	// added are cleaned up first. We don't return an error right away
	// so that other resources still get cleaned up in the case of one
	// or more failing.
	for i := len(r.Resources) - 1; i >= 0; i-- {
		switch r.Resources[i].(type) {
		case *uvm.NetworkEndpoints:
			if r.CreatedNetNS {
				if err := r.Resources[i].Release(ctx); err != nil {
					log.G(ctx).WithError(err).Error("failed to release container resource")
					releaseErr = true
				}
				r.CreatedNetNS = false
			}
		case *CCGInstance:
			if err := r.Resources[i].Release(ctx); err != nil {
				log.G(ctx).WithError(err).Error("failed to release container resource")
				releaseErr = true
			}
		default:
			// Don't need to check if vm != nil here anymore as they wouldnt
			// have been added in the first place. All resources have embedded
			// vm they belong to.
			if all {
				if err := r.Resources[i].Release(ctx); err != nil {
					log.G(ctx).WithError(err).Error("failed to release container resource")
					releaseErr = true
				}
			}
		}
	}
	r.Resources = nil
	if releaseErr {
		return errors.New("failed to release one or more container resources")
	}

	// cleanup container state
	if vm != nil {
		if vm.DeleteContainerStateSupported() {
			if err := vm.DeleteContainerState(ctx, r.ID); err != nil {
				log.G(ctx).WithError(err).Error("failed to delete container state")
			}
		}
	}

	if r.Layers != nil {
		// TODO dcantah: Either make it so layers doesn't rely on the all bool for cleanup logic
		// or find a way to factor out the all bool in favor of something else.
		if err := r.Layers.Release(ctx, all); err != nil {
			return err
		}
	}
	return nil
}

func containerRootfsPath(uvm *uvm.UtilityVM, rootPath string) string {
	if uvm.OS() == "windows" {
		return ospath.Join(uvm.OS(), rootPath)
	}
	return ospath.Join(uvm.OS(), rootPath, RootfsPath)
}

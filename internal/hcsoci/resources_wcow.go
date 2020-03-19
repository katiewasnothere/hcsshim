// +build windows

package hcsoci

// Contains functions relating to a WCOW container, as opposed to a utility VM

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/oci"
	hcsschema "github.com/Microsoft/hcsshim/internal/schema2"
	"github.com/Microsoft/hcsshim/internal/schemaversion"
	"github.com/Microsoft/hcsshim/internal/shimdiag"
	"github.com/Microsoft/hcsshim/internal/uvm"
	"github.com/Microsoft/hcsshim/internal/wclayer"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

const wcowGlobalMountPath = "C:\\mounts\\"
const wcowGlobalMountPrefix = wcowGlobalMountPath + "m%d"
const wcowGlobalDriverMountPath = wcowGlobalMountPath + "drivers"

func getAssignedDeviceKernelDrivers(coi *createOptionsInternal) ([]string, error) {
	drivers, ok := coi.Spec.Annotations[oci.AnnotationAssignedDeviceKernelDrivers]
	if !ok || drivers == "" {
		return nil, fmt.Errorf("no assigned device drivers specified %s", drivers)
	}
	return strings.Split(drivers, ","), nil

}

func allocateWindowsResources(ctx context.Context, coi *createOptionsInternal, resources *Resources) (err error) {
	if coi.Spec == nil || coi.Spec.Windows == nil || coi.Spec.Windows.LayerFolders == nil {
		return fmt.Errorf("field 'Spec.Windows.Layerfolders' is not populated")
	}

	scratchFolder := coi.Spec.Windows.LayerFolders[len(coi.Spec.Windows.LayerFolders)-1]

	// TODO: Remove this code for auto-creation. Make the caller responsible.
	// Create the directory for the RW scratch layer if it doesn't exist
	if _, err := os.Stat(scratchFolder); os.IsNotExist(err) {
		if err := os.MkdirAll(scratchFolder, 0777); err != nil {
			return fmt.Errorf("failed to auto-create container scratch folder %s: %s", scratchFolder, err)
		}
	}

	// Create sandbox.vhdx if it doesn't exist in the scratch folder. It's called sandbox.vhdx
	// rather than scratch.vhdx as in the v1 schema, it's hard-coded in HCS.
	if _, err := os.Stat(filepath.Join(scratchFolder, "sandbox.vhdx")); os.IsNotExist(err) {
		if err := wclayer.CreateScratchLayer(ctx, scratchFolder, coi.Spec.Windows.LayerFolders[:len(coi.Spec.Windows.LayerFolders)-1]); err != nil {
			return fmt.Errorf("failed to CreateSandboxLayer %s", err)
		}
	}

	if coi.Spec.Root == nil {
		coi.Spec.Root = &specs.Root{}
	}

	if coi.Spec.Root.Path == "" && (coi.HostingSystem != nil || coi.Spec.Windows.HyperV == nil) {
		log.G(ctx).Debug("hcsshim::allocateWindowsResources mounting storage")
		containerRootPath, err := MountContainerLayers(ctx, coi.Spec.Windows.LayerFolders, resources.containerRootInUVM, coi.HostingSystem)
		if err != nil {
			return fmt.Errorf("failed to mount container storage: %s", err)
		}
		if coi.HostingSystem == nil {
			coi.Spec.Root.Path = containerRootPath // Argon v1 or v2
		} else {
			coi.Spec.Root.Path = containerRootPath // v2 Xenon WCOW
		}
		resources.layers = coi.Spec.Windows.LayerFolders
	}

	// Validate each of the mounts. If this is a V2 Xenon, we have to add them as
	// VSMB shares to the utility VM. For V1 Xenon and Argons, there's nothing for
	// us to do as it's done by HCS.
	for i, mount := range coi.Spec.Mounts {
		if mount.Destination == "" || mount.Source == "" {
			return fmt.Errorf("invalid OCI spec - a mount must have both source and a destination: %+v", mount)
		}
		switch mount.Type {
		case "":
		case "physical-disk":
		case "virtual-disk":
		case "automanage-virtual-disk":
		default:
			return fmt.Errorf("invalid OCI spec - Type '%s' not supported", mount.Type)
		}

		if coi.HostingSystem != nil && schemaversion.IsV21(coi.actualSchemaVersion) {
			uvmPath := fmt.Sprintf("C:\\%s\\%d", coi.actualID, i)

			readOnly := false
			for _, o := range mount.Options {
				if strings.ToLower(o) == "ro" {
					readOnly = true
					break
				}
			}
			l := log.G(ctx).WithField("mount", fmt.Sprintf("%+v", mount))
			if mount.Type == "physical-disk" {
				l.Debug("hcsshim::allocateWindowsResources Hot-adding SCSI physical disk for OCI mount")
				_, _, _, err := coi.HostingSystem.AddSCSIPhysicalDisk(ctx, mount.Source, uvmPath, readOnly)
				if err != nil {
					return fmt.Errorf("adding SCSI physical disk mount %+v: %s", mount, err)
				}
				coi.Spec.Mounts[i].Type = ""
				resources.scsiMounts = append(resources.scsiMounts, scsiMount{path: mount.Source})
			} else if mount.Type == "virtual-disk" || mount.Type == "automanage-virtual-disk" {
				l.Debug("hcsshim::allocateWindowsResources Hot-adding SCSI virtual disk for OCI mount")
				_, _, _, err := coi.HostingSystem.AddSCSI(ctx, mount.Source, uvmPath, readOnly)
				if err != nil {
					return fmt.Errorf("adding SCSI virtual disk mount %+v: %s", mount, err)
				}
				coi.Spec.Mounts[i].Type = ""
				resources.scsiMounts = append(resources.scsiMounts, scsiMount{path: mount.Source, autoManage: mount.Type == "automanage-virtual-disk"})
			} else {
				if uvm.IsPipe(mount.Source) {
					if err := coi.HostingSystem.AddPipe(ctx, mount.Source); err != nil {
						return fmt.Errorf("failed to add named pipe to UVM: %s", err)
					}
					resources.pipeMounts = append(resources.pipeMounts, mount.Source)
				} else {
					l.Debug("hcsshim::allocateWindowsResources Hot-adding VSMB share for OCI mount")
					options := &hcsschema.VirtualSmbShareOptions{}
					if readOnly {
						options.ReadOnly = true
						options.CacheIo = true
						options.ShareRead = true
						options.ForceLevelIIOplocks = true
						break
					}
					if coi.HostingSystem.GetDeviceBackingType() == uvm.PhysicalBacking {
						options.NoDirectmap = true
					}

					if err := coi.HostingSystem.AddVSMB(ctx, mount.Source, "", options); err != nil {
						return fmt.Errorf("failed to add VSMB share to utility VM for mount %+v: %s", mount, err)
					}
					resources.vsmbMounts = append(resources.vsmbMounts, mount.Source)
				}

			}
		}
	}

	if coi.HostingSystem != nil {
		err = installDrivers(ctx, coi, resources)
		if err != nil {
			return err
		}
		err := handleAssignedDevices(ctx, coi, resources)
		if err != nil {
			return err
		}
	}

	return nil
}

func handleAssignedDevices(ctx context.Context, coi *createOptionsInternal, resources *Resources) error {
	vpciVMBusInstanceIDs := []string{}
	for _, d := range coi.Spec.Windows.Devices {
		if d.IDType == uvm.VPCIDeviceIDType {
			device := hcsschema.VirtualPciDevice{
				Functions: []hcsschema.VirtualPciFunction{
					{
						DeviceInstancePath: d.ID,
					},
				},
			}
			vmBusGUID, err := coi.HostingSystem.AssignDevice(ctx, device)
			if err != nil {
				return errors.Wrapf(err, "failed to assign device %s of type %s to pod %s", d.ID, d.IDType, coi.HostingSystem.ID())
			}
			resources.vpciDevices = append(resources.vpciDevices, vmBusGUID)
			// TODO katiewasnothere: for now use the vmbus class guid so we can make progress....
			/*coi.Spec.Windows.Devices[i].ID = "4D36E97D-E325-11CE-BFC1-08002BE10318"
			coi.Spec.Windows.Devices[i].IDType = "InterfaceClassGUID"*/

			vmBusInstanceID := coi.HostingSystem.GetAssignedDeviceParentID(vmBusGUID)
			vpciVMBusInstanceIDs = append(vpciVMBusInstanceIDs, vmBusInstanceID)
		}
	}

	assignedDevices := []specs.WindowsDevice{}
	if len(vpciVMBusInstanceIDs) != 0 {
		busDevices, err := getBusAssignedDevices(ctx, coi, vpciVMBusInstanceIDs, uvm.VPCILocationPathIDType)
		if err != nil {
			return err
		}
		assignedDevices = append(assignedDevices, busDevices...)
	}
	// override devices with uvm specific device identifiers
	coi.Spec.Windows.Devices = assignedDevices
	return nil
}

// mountDrivers mounts all added driver files into the uvm under wcowGlobalDriverMountPath
func mountDrivers(ctx context.Context, vm *uvm.UtilityVM, resources *Resources, driverHostPaths []string) error {
	readOnly := true
	for _, d := range driverHostPaths {
		if _, err := os.Stat(d); err != nil {
			return err
		}
		uvmPath := filepath.Join(wcowGlobalDriverMountPath, d)
		_, _, _, err := vm.AddSCSI(ctx, d, uvmPath, readOnly)
		if err != nil {
			return err
		}
		resources.scsiMounts = append(resources.scsiMounts, scsiMount{path: d, autoManage: false})
	}
	return nil
}

// TODO katiewasnothere: do I really want to add ALL drivers in this dir? can i specify
// without making multiple exec calls?

// execPnPInstallAllDrivers makes the call to exec in the uvm the pnp command
// that installs all drivers under wcowGlobalDriverMountPath
func execPnPInstallAllDrivers(ctx context.Context, vm *uvm.UtilityVM) error {
	args := []string{"pnputil.exe", "/add-driver", "*.inf", "/subdirs", "/install"}
	req := &shimdiag.ExecProcessRequest{
		Args:    args,
		Workdir: wcowGlobalDriverMountPath,
	}
	exitCode, err := ExecInUvm(ctx, vm, req)
	if err != nil {
		return errors.Wrapf(err, "failed to install drivers in uvm with exit code %d", exitCode)
	}
	return nil
}

// TODO katiewasnothere: check that I can have two containers try to mount this
func installDrivers(ctx context.Context, coi *createOptionsInternal, resources *Resources) error {
	drivers, err := getAssignedDeviceKernelDrivers(coi)
	if err != nil {
		return err
	}
	err = mountDrivers(ctx, coi.HostingSystem, resources, drivers)
	if err != nil {
		return err
	}
	err = execPnPInstallAllDrivers(ctx, coi.HostingSystem)
	if err != nil {
		return err
	}
	return nil
}

func getBusAssignedDevicesWIP(ctx context.Context, coi *createOptionsInternal, vmBusGUIDs []string, idType string) ([]specs.WindowsDevice, error) {
	result := []specs.WindowsDevice{}
	out := os.Stdout
	formattedVMBusGUIDs := strings.Join(vmBusGUIDs, ",")
	parentIDsFlag := fmt.Sprintf("--parentID='%s'", formattedVMBusGUIDs)
	args := []string{"C:\\device-util.exe", "children", parentIDsFlag, "--property=location"}
	req := &shimdiag.ExecProcessRequest{
		Args:    args,
		Workdir: "C:\\",
		Stdout:  out.Name(),
	}
	exitCode, err := ExecInUvm(ctx, coi.HostingSystem, req)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to find devices with exit code %d", exitCode)
	}
	r := bufio.NewReader(out)
	var readErr error
	var devicePath []byte
	for readErr != io.EOF {
		devicePath, readErr = r.ReadSlice(',')
		if len(devicePath) != 0 {
			// remove the comma at the end of the line
			id := string(devicePath[:len(devicePath)-1])
			specDev := specs.WindowsDevice{
				ID:     id,
				IDType: idType,
			}
			result = append(result, specDev)
		}
	}
	return result, nil
}

func buildPnPEnumDevicesArgs(id string) []string {
	return []string{"pnputil.exe", "/enum-devices", "/instanceid", id, "/relations", ";"}
}

// getBusAssignedDevices batches the command to find parent devices' children in the uvm
// then executes it in the uvm and returns a slice of the resulting child device instance IDs.
// This assumes that we only assign one virtual function to each vpci device assignment request we've made.
func getBusAssignedDevices(ctx context.Context, coi *createOptionsInternal, vmbusInstanceIDs []string, idType string) ([]specs.WindowsDevice, error) {
	result := []specs.WindowsDevice{}
	out := os.Stdout
	var args []string
	for _, id := range vmbusInstanceIDs {
		idArgs := buildPnPEnumDevicesArgs(id)
		args = append(args, idArgs...)
	}
	req := &shimdiag.ExecProcessRequest{
		Args:    args,
		Workdir: "C:\\",
		Stdout:  out.Name(),
	}
	exitCode, err := ExecInUvm(ctx, coi.HostingSystem, req)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to find devices with exit code %d", exitCode)
	}

	r := bufio.NewReader(out)
	var readErr error
	var line []byte
	for readErr != io.EOF {
		line, _, readErr = r.ReadLine()
		lineAsString := string(line)
		if strings.HasPrefix(lineAsString, "Children:") {
			devicePath := strings.TrimSpace(strings.TrimPrefix(lineAsString, "Children:"))
			specDev := specs.WindowsDevice{
				ID:     devicePath,
				IDType: idType,
			}
			result = append(result, specDev)
		}
	}
	return result, nil
}

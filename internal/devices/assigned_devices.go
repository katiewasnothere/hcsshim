// +build windows

package devices

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"strings"

	winio "github.com/Microsoft/go-winio"
	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/resources"
	"github.com/Microsoft/hcsshim/internal/shimdiag"
	"github.com/Microsoft/hcsshim/internal/uvm"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

// HandleAssignedDevicesWindows does all of the work to setup the hosting UVM and retrieve
// device information for adding assigned devices on a WCOW container definition.
//
// First, devices are assigned into the hosting UVM. Drivers are then added into the UVM and
// installed on the matching devices. This ordering allows us to guarantee that driver
// installation on a device in the UVM is completed before we attempt to create a container.
//
// Then we find the location paths of the target devices in the UVM and return the results
// as WindowsDevices.
func HandleAssignedDevicesWindows(ctx context.Context, vm *uvm.UtilityVM, windowsDevices []specs.WindowsDevice, annotations map[string]string) (resources []resources.ResourceCloser, resultDevices []specs.WindowsDevice, err error) {
	defer func() {
		if err != nil {
			for _, r := range resources {
				r.Release(ctx)
			}
		}
	}()
	vpciVMBusInstanceIDs, err := assignWindowsDevices(ctx, vm, windowsDevices, resources)
	if err != nil {
		return resources, nil, err
	}

	if err := installWindowsDrivers(ctx, vm, annotations, resources); err != nil {
		return resources, nil, err
	}
	deviceUtilPath, err := setupDeviceUtilTool(ctx, vm, annotations, resources)
	if err != nil {
		return resources, nil, err
	}

	resultDevices, err = getChildrenDeviceLocationPaths(ctx, vm, vpciVMBusInstanceIDs, deviceUtilPath)
	if err != nil {
		return resources, nil, err
	}

	return resources, resultDevices, nil
}

func assignWindowsDevices(ctx context.Context, vm *uvm.UtilityVM, windowsDevices []specs.WindowsDevice, resources []resources.ResourceCloser) ([]string, error) {
	vpciVMBusInstanceIDs := []string{}
	for _, d := range windowsDevices {
		if d.IDType == uvm.VPCIDeviceIDType || d.IDType == uvm.VPCIDeviceIDTypeLegacy {
			vpci, err := vm.AssignDevice(ctx, d.ID)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to assign device %s of type %s to pod %s", d.ID, d.IDType, vm.ID())
			}
			resources = append(resources, vpci)
			vmBusInstanceID := vm.GetAssignedDeviceParentID(vpci.VMBusGUID)
			log.G(ctx).WithField("vmbus id", vmBusInstanceID).Info("vmbus instance ID")

			vpciVMBusInstanceIDs = append(vpciVMBusInstanceIDs, vmBusInstanceID)
		} else {
			return nil, fmt.Errorf("device type %s for device %s is not supported on windows", d.IDType, d.ID)
		}
	}
	return vpciVMBusInstanceIDs, nil
}

// getChildrenDeviceLocationPaths queries the UVM with the device-util tool with the formatted
// parent bus device for the children devices' location paths from the uvm's view.
// Returns a slice of WindowsDevices created from the resulting children location paths
func getChildrenDeviceLocationPaths(ctx context.Context, vm *uvm.UtilityVM, vmBusInstanceIDs []string, deviceUtilPath string) ([]specs.WindowsDevice, error) {
	p, l, err := createNamedPipeListener()
	if err != nil {
		return nil, err
	}
	defer l.Close()

	var pipeResults []string
	errChan := make(chan error)

	go readCsPipeOutput(l, errChan, &pipeResults)

	args := createDeviceUtilChildrenCommand(deviceUtilPath, vmBusInstanceIDs)
	req := &shimdiag.ExecProcessRequest{
		Args:   args,
		Stdout: p,
	}
	exitCode, err := shimdiag.ExecInUvm(ctx, vm, req)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to find devices with exit code %d", exitCode)
	}

	// wait to finish parsing stdout results
	select {
	case err := <-errChan:
		if err != nil {
			return nil, err
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// convert stdout results into windows devices
	results := []specs.WindowsDevice{}
	for _, value := range pipeResults {
		specDev := specs.WindowsDevice{
			ID:     value,
			IDType: uvm.VPCILocationPathIDType,
		}
		results = append(results, specDev)
	}
	log.G(ctx).WithField("parsed devices", results).Info("found child assigned devices")
	return results, nil
}

func createNamedPipeListener() (string, net.Listener, error) {
	g, err := guid.NewV4()
	if err != nil {
		return "", nil, err
	}
	p := `\\.\pipe\` + g.String()
	l, err := winio.ListenPipe(p, nil)
	if err != nil {
		return "", nil, err
	}
	return p, l, nil
}

// readCsPipeOutput is a helper function that connects to a listener and reads
// the connection's comma separated outut until done. resulting comma separated
// values are returned in the `result` param. The `done` param is used by this
// func to indicate completion.
func readCsPipeOutput(l net.Listener, errChan chan<- error, result *[]string) {
	defer close(errChan)
	c, err := l.Accept()
	if err != nil {
		errChan <- errors.Wrapf(err, "failed to accept named pipe")
		return
	}
	bytes, err := ioutil.ReadAll(c)
	if err != nil {
		errChan <- err
		return
	}

	elementsAsString := strings.TrimSuffix(string(bytes), "\n")
	elements := strings.Split(elementsAsString, ",")

	for _, elem := range elements {
		*result = append(*result, elem)
	}

	if len(*result) == 0 {
		errChan <- errors.Wrapf(err, "failed to get any pipe output")
		return
	}

	errChan <- nil
}

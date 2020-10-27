package uvm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Microsoft/hcsshim/internal/guestrequest"
	"github.com/Microsoft/hcsshim/internal/requesttype"
	hcsschema "github.com/Microsoft/hcsshim/internal/schema2"
)

func (uvm *UtilityVM) Share(ctx context.Context, reqHostPath, reqUVMPath string, readOnly bool) error {
	if uvm.OS() == "windows" {
		if _, err := uvm.GetVSMBUvmPath(ctx, reqHostPath, readOnly); err == ErrNotAttached {
			// share file has not been added yet, add it now
			options := uvm.DefaultVSMBOptions(readOnly)
			_, err := uvm.AddVSMB(ctx, reqHostPath, options)
			if err != nil {
				return err
			}
			sharePath, err := uvm.GetVSMBUvmPath(ctx, reqHostPath, readOnly)
			if err != nil {
				return err
			}
			guestReq := guestrequest.GuestRequest{
				ResourceType: guestrequest.ResourceTypeMappedDirectory,
				RequestType:  requesttype.Add,
				Settings: &hcsschema.MappedDirectory{
					HostPath:      sharePath,
					ContainerPath: reqUVMPath,
					ReadOnly:      readOnly,
				},
			}
			if err := uvm.GuestRequest(ctx, guestReq); err != nil {
				return err
			}
		}
	} else {
		st, err := os.Stat(reqHostPath)
		if err != nil {
			return fmt.Errorf("could not open '%s' path on host: %s", reqHostPath, err)
		}
		var (
			hostPath       string = reqHostPath
			restrictAccess bool
			fileName       string
			allowedNames   []string
		)
		if !st.IsDir() {
			hostPath, fileName = filepath.Split(hostPath)
			allowedNames = append(allowedNames, fileName)
			restrictAccess = true
		}
		_, err = uvm.AddPlan9(ctx, hostPath, reqUVMPath, readOnly, restrictAccess, allowedNames)
		if err != nil {
			return err
		}
	}
	return nil
}

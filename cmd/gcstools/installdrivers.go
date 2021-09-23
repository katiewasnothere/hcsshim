// +build linux

package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Microsoft/hcsshim/internal/guest/storage/overlay"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	lcowGlobalDriversPrefix = "/run/drivers/%s"

	moduleExtension = ".ko"
)

func install(ctx context.Context) error {
	args := []string(os.Args[1:])

	if len(args) == 0 {
		return errors.New("no driver paths provided for install")
	}

	for _, driver := range args {
		modules := []string{}

		driverGUID, err := uuid.NewRandom()
		if err != nil {
			return err
		}

		// create an overlay mount from the driver's UVM path so we can write to the
		// mount path in the UVM despite having mounted in the driver originally as
		// readonly
		runDriverPath := fmt.Sprintf(lcowGlobalDriversPrefix, driverGUID.String())
		upperPath := filepath.Join(runDriverPath, "upper")
		workPath := filepath.Join(runDriverPath, "work")
		rootPath := filepath.Join(runDriverPath, "content")
		if err := overlay.Mount(ctx, []string{driver}, upperPath, workPath, rootPath, false); err != nil {
			return err
		}

		// get list module names without .ko extension
		if walkErr := filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return errors.Wrap(err, "failed to read directory while walking dir")
			}
			if !d.IsDir() && filepath.Ext(d.Name()) == moduleExtension {
				moduleName := strings.TrimSuffix(d.Name(), moduleExtension)
				modules = append(modules, moduleName)
				fmt.Fprintln(os.Stderr, moduleName)
			}
			return nil
		}); walkErr != nil {
			return walkErr
		}

		// create a new module dependency map database for the driver
		depmodArgs := []string{"-b", rootPath}
		cmd := exec.Command("depmod", depmodArgs...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return errors.Wrapf(err, "failed to run cmd with message: %s", out)
		}

		// run modprobe for every module name found
		modprobeArgs := append([]string{"-d", rootPath, "-a"}, modules...)
		cmd = exec.Command(
			"modprobe",
			modprobeArgs...,
		)

		out, err = cmd.CombinedOutput()
		if err != nil {
			return errors.Wrapf(err, "failed to run cmd with message: %s", out)
		}
	}

	return nil
}

func installDriversMain() {
	ctx := context.Background()
	if err := install(ctx); err != nil {
		logrus.Errorf("error in install drivers: %s", err)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

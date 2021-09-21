package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Microsoft/hcsshim/internal/guest/storage/overlay"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const lcowGlobalDriversPrefix = "/run/drivers/%s"

func install(ctx context.Context) error {
	args := []string(os.Args[1:])

	if len(args) == 0 {
		return errors.New("no driver paths provided for install")
	}

	for _, driver := range args {
		modules := []string{}

		b := make([]byte, 16)
		_, err := rand.Read(b)
		if err != nil {
			return err
		}
		driverGUID := fmt.Sprintf("%x-%x-%x-%x-%x",
			b[0:4], b[4:6], b[6:8], b[8:10], b[10:])

		runDriverPath := fmt.Sprintf(lcowGlobalDriversPrefix, driverGUID)
		upperPath := filepath.Join(runDriverPath, "upper")
		workPath := filepath.Join(runDriverPath, "work")
		rootPath := filepath.Join(runDriverPath, "content")
		if err := overlay.MountOverlay(ctx, []string{driver}, upperPath, workPath, rootPath, false); err != nil {
			return err
		}

		// get list module names without .ko extension
		if walkErr := filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return errors.Wrap(err, "failed to read directory while walking dir")
			}
			if !d.IsDir() && filepath.Ext(d.Name()) == ".ko" {
				moduleName := strings.TrimSuffix(d.Name(), ".ko")
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
			logrus.Errorf("error in install drivers: %s", err)
			fmt.Fprintln(os.Stderr, err)
			return errors.Wrapf(err, "failed to run cmd with message: %s", out)
		}
	}

	return nil
}

func installDriversMain() {
	ctx := context.Background()
	logFileHandle, err := os.OpenFile("/tmp/installdebug.txt", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0700)
	if err != nil {
		logrus.Errorf("error in install drivers: %s", err)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	logrus.SetOutput(logFileHandle)
	if err := install(ctx); err != nil {
		logrus.Errorf("error in install drivers: %s", err)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

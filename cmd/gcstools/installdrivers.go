package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const libModulesFormat = "/lib/modules/%s/kernel"

var unixMount = unix.Mount

func install() error {
	args := []string(os.Args[1:])

	if len(args) == 0 {
		return errors.New("no driver paths provided for install")
	}

	// get the uname for the running kernel
	uts := &unix.Utsname{}
	if err := unix.Uname(uts); err != nil {
		return errors.Wrap(err, "failed to get system uname information")
	}

	valueStr := strings.Replace(string(uts.Release[:]), "\x00", "", -1)
	moduleDir := fmt.Sprintf(libModulesFormat, valueStr)

	if _, err := os.Stat(moduleDir); err != nil {
		if err := os.MkdirAll(moduleDir, 0700); err != nil {
			return err
		}
		return err
	}

	// get path that the drivers were installed to
	// create modules directory with correct kernel version if not already exists
	// mount modules dir from drivers mount into /lib/modules

	modules := []string{}
	for _, driver := range args {
		// mount driver modules into UVM's module path
		// TODO: katiewasnothere: is this a good name?
		driverName := filepath.Base(driver)
		driverModulePath := driver + moduleDir
		_, err := os.Stat(driverModulePath)
		if os.IsNotExist(err) {
			return errors.Wrapf(err, "no matching modules directory \"%s\" found in driver", driverModulePath)
		} else if err != nil {
			return errors.Wrap(err, "failed to stat the driver's modules")
		}

		// create a new directory in the main modules directory
		targetModuleDirPath := filepath.Join(moduleDir, driverName)
		_, err = os.Stat(targetModuleDirPath)
		if os.IsNotExist(err) {
			if err := os.MkdirAll(targetModuleDirPath, 0700); err != nil {
				return errors.Wrap(err, "failed to make the modules directory")
			}
		} else if err != nil {
			return errors.Wrap(err, "failed to get the driver dir")
		}

		// bind mount the drivers modules into the main modules directory
		if err := unixMount(driverModulePath, targetModuleDirPath, "", syscall.MS_BIND, ""); err != nil {
			return errors.Wrapf(err, "failed to mount driver module %s and %s", driverModulePath, moduleDir)
		}

		// get list module names without .ko extension
		if walkErr := filepath.WalkDir(driverModulePath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return errors.Wrap(err, "failed to read directory while walking dir")
			}
			if !d.IsDir() && strings.HasPrefix(d.Name(), ".ko") {
				moduleName := strings.TrimSuffix(d.Name(), ".ko")
				modules = append(modules, moduleName)
			}
			return nil
		}); walkErr != nil {
			return walkErr
		}

	}

	// create a new module dependency map database
	cmd := exec.Command("depmod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "failed to run cmd with message: %s", out)
	}

	// run modprobe for every module name found
	args = append([]string{"-a"}, modules...)
	cmd = exec.Command(
		"modprobe",
		args...,
	)
	out, err = cmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "failed to run cmd with message: %s", out)
	}
	return nil
}

func installDriversMain() {
	logFileHandle, err := os.OpenFile("installdebug.txt", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0700)
	if err != nil {
		logrus.Errorf("error in install drivers: %s", err)
		os.Exit(1)
	}
	logrus.SetOutput(logFileHandle)
	if err := install(); err != nil {
		logrus.Errorf("error in install drivers: %s", err)
		os.Exit(1)
	}
}

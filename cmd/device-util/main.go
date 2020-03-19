package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Microsoft/hcsshim/internal/cfgmgr"
	"github.com/urfave/cli"
)

var usage = `deviceutil is a command line tool for querying devices present in a Windows UVM`

func main() {
	app := cli.NewApp()
	app.Name = "deviceutil"
	app.Commands = []cli.Command{
		queryCommand,
		getChildrenCommand,
	}
	app.Usage = usage

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var (
	enumeratorFlag = "enumerator"
	propertyFlag   = "property"

	locationProperty = "location"
	idProperty       = "id"
)

var queryCommand = cli.Command{
	Name:  "query",
	Usage: "queries for present devices on the system",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  enumeratorFlag,
			Usage: "Device enumerator to query for devices.",
		},
		cli.StringFlag{
			Name:  propertyFlag,
			Usage: "Either 'location' or 'id', default 'id'. String indicating a property to query devices for.",
		},
	},
	Action: func(context *cli.Context) error {
		enumerator := ""
		if context.IsSet(enumeratorFlag) {
			enumerator = context.String(enumeratorFlag)
		}

		var deviceIDs []string
		var err error
		if enumerator == "" {
			deviceIDs, err = cfgmgr.GetDeviceIDListAllPresent()
		} else {
			deviceIDs, err = cfgmgr.GetDeviceIDListFromEnumerator(enumerator)
		}
		if err != nil {
			return err
		}

		property := ""
		if context.IsSet(propertyFlag) {
			property = context.String(propertyFlag)
		}

		if property == locationProperty {
			locationPaths, err := cfgmgr.GetDeviceLocationPathsFromIDs(deviceIDs)
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, locationPaths)
			return nil
		}
		fmt.Fprintln(os.Stdout, deviceIDs)
		return nil
	},
}

var (
	parentIDFlag = "parentID"
)

var getChildrenCommand = cli.Command{
	Name:  "children",
	Usage: "queries for given devices' children on the system",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  parentIDFlag,
			Usage: "Required: Parent device's instance IDs. Comma separated string.",
		},
		cli.StringFlag{
			Name:  propertyFlag,
			Usage: "Either 'location' or 'id', default 'id'. String indicating a property to query devices for.",
		},
	},
	Action: func(context *cli.Context) error {
		if !context.IsSet(parentIDFlag) {
			return errors.New("`children` command must specify at least one parent instance ID")
		}
		csParents := context.String(parentIDFlag)
		parents := strings.Split(csParents, ",")

		children, err := cfgmgr.GetChildrenFromInstanceIDs(parents)
		if err != nil {
			return err
		}

		property := ""
		if context.IsSet(propertyFlag) {
			property = context.String(propertyFlag)
		}
		if property == locationProperty {
			locationPaths, err := cfgmgr.GetDeviceLocationPathsFromIDs(children)
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, locationPaths)
			return nil
		}
		formattedChildren := strings.Join(children, ",")
		fmt.Fprintln(os.Stdout, formattedChildren)
		return nil
	},
}

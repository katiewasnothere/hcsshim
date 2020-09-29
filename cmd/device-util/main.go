package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Microsoft/hcsshim/internal/jobs"

	"github.com/Microsoft/hcsshim/internal/windevice"
	"github.com/Microsoft/hcsshim/internal/winobjdir"
	"github.com/urfave/cli"
)

const usage = `device-util is a command line tool for querying devices present on Windows`

func main() {
	app := cli.NewApp()
	app.Name = "device-util"
	app.Commands = []cli.Command{
		queryChildrenCommand,
		readObjDirCommand,
		queryJobObjCommand,
		setJobObjectLimitsCommand,
	}
	app.Usage = usage

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

const (
	parentIDFlag = "parentID"
	propertyFlag = "property"
	objDirFlag   = "dir"

	locationProperty = "location"
	idProperty       = "id"

	globalNTPath = "\\Global??"
)

var readObjDirCommand = cli.Command{
	Name:  "obj-dir",
	Usage: "outputs contents of a NT object directory",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  objDirFlag,
			Usage: "Optional: Object directory to query. Defaults to the global object directory.",
		},
	},
	Action: func(context *cli.Context) error {
		dir := globalNTPath
		if context.IsSet(objDirFlag) {
			dir = context.String(objDirFlag)
		}
		entries, err := winobjdir.EnumerateNTObjectDirectory(dir)
		if err != nil {
			return err
		}
		formatted := strings.Join(entries, ",")
		fmt.Fprintln(os.Stdout, formatted)
		return nil
	},
}

var queryChildrenCommand = cli.Command{
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

		children, err := windevice.GetChildrenFromInstanceIDs(parents)
		if err != nil {
			return err
		}

		property := idProperty
		if context.IsSet(propertyFlag) {
			property = context.String(propertyFlag)
		}
		if property == locationProperty {
			children, err = windevice.GetDeviceLocationPathsFromIDs(children)
			if err != nil {
				return err
			}
		}
		formattedChildren := strings.Join(children, ",")
		fmt.Fprintln(os.Stdout, formattedChildren)
		return nil
	},
}

var queryJobObjCommand = cli.Command{
	Name:  "query-jobobj",
	Usage: "queries for given container's resources",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "containerID",
			Usage: "Required: Container ID to get the job object information for.",
		},
		cli.BoolFlag{
			Name:  "processorCount",
			Usage: "Optional: specify that you want to query for the job object's cpu rate and translate into container processor count",
		},
		cli.BoolFlag{
			Name: "processorWeight",
			Usage: `Optional: specify that you want to query for the job object's cpu weight and translate into container processor weight.
			Note: Due to rounding issues as a result of the job object's cpu weight being represented as an integer, 
			the resulting processor weight may not correspond to the same processor weight initially set.`,
		},
		cli.BoolFlag{
			Name:  "memoryLimitInMB",
			Usage: "Optional: specify that you want to query the job object's memory limit and convert to the limit in MBs.",
		},
		cli.BoolFlag{
			Name:  "affinity",
			Usage: "Optional: specify that you want to query the job object's affinity as a bitmask.",
		},
	},
	Action: func(cli *cli.Context) error {
		ctx := context.Background()
		if !cli.IsSet("containerID") {
			return errors.New("`query-jobobj` command must specify a target container ID")
		}
		containerID := cli.String("containerID")
		if cli.IsSet("processorCount") {
			cpuRate, err := jobs.GetJobObjectCPURate(containerID)
			if err != nil {
				return err
			}
			procCount, err := jobs.CalculateProcessorCount(ctx, cpuRate)
			if err != nil {
				return err
			}
			output := fmt.Sprintf("processorCount: %d", procCount)
			fmt.Fprintln(os.Stdout, output)
		} else if cli.IsSet("processorWeight") {
			cpuWeight, err := jobs.GetJobObjectCPUWeight(containerID)
			if err != nil {
				return err
			}
			initial := fmt.Sprintf("cpuWeight: %d", cpuWeight)
			fmt.Fprintln(os.Stdout, initial)
			procWeight, err := jobs.CalculateProcessorWeight(cpuWeight)
			if err != nil {
				return err
			}
			output := fmt.Sprintf("processorWeight: %d", procWeight)
			fmt.Fprintln(os.Stdout, output)
		} else if cli.IsSet("memoryLimitInMB") {
			jobObjMemLimit, err := jobs.GetJobObjectMemoryLimit(containerID)
			if err != nil {
				return err
			}
			memInMB := jobs.CalculateMemoryInMB(jobObjMemLimit)
			output := fmt.Sprintf("memoryLimitInMB: %d", memInMB)
			fmt.Fprintln(os.Stdout, output)
		} else if cli.IsSet("affinity") {
			affinity, err := jobs.GetJobObjectAffinity(containerID)
			if err != nil {
				return err
			}
			affinityString := strconv.FormatUint(affinity, 2)
			output := fmt.Sprintf("affinity: %s", affinityString)
			fmt.Fprintln(os.Stdout, output)
		}
		// TODO katiewasnothere: make sure this is a WCOW container or IDK what would happen

		return nil
	},
}

var setJobObjectLimitsCommand = cli.Command{
	Name:  "set-jobobj",
	Usage: "tool used to set resource limits on container job objects",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "containerID",
			Usage: "Required: Container ID to get the job object information for.",
		},
		cli.Uint64Flag{
			Name:  "processorLimit",
			Usage: "Optional: specify that you want to set the cpu rate resource for the job object.",
		},
		cli.Uint64Flag{
			Name:  "processorCount",
			Usage: "Optional: specify that you want to set number of processors for the job object.",
		},
		cli.Uint64Flag{
			Name:  "processorWeight",
			Usage: "Optional: specify that you want to set the job object's cpu weight.",
		},
		cli.Uint64Flag{
			Name:  "memoryLimitInMB",
			Usage: "Optional: specify that you want to set the job object's memory limit in MB.",
		},
		cli.StringFlag{
			Name:  "affinity",
			Usage: "Optional: specify that you want to set the job object's affinity. Takes in a string representatioin of the desired affinity bitmask.",
		},
	},
	Action: func(cli *cli.Context) error {
		ctx := context.Background()
		if !cli.IsSet("containerID") {
			return errors.New("`set-jobobj` command must specify a target container ID")
		}
		containerID := cli.String("containerID")
		if cli.IsSet("processorLimit") {
			cpuRate := uint32(cli.Uint64("processorLimit"))
			return jobs.SetJobObjectCPURate(containerID, cpuRate)
		} else if cli.IsSet("processorCount") {
			procs := uint32(cli.Uint64("procCount"))
			cpuRate, err := jobs.CalculateJobCPURate(ctx, procs)
			if err != nil {
				return err
			}
			return jobs.SetJobObjectCPURate(containerID, cpuRate)
		} else if cli.IsSet("processorWeight") {
			procWeight := uint32(cli.Uint64("processorWeight"))
			cpuWeight := jobs.CalculateJobCPUWeight(procWeight)
			return jobs.SetJobObjectCPUWeight(containerID, cpuWeight)
		} else if cli.IsSet("memoryLimitInMB") {
			memLimitInMB := cli.Uint64("memoryLimitInMB")
			jobMemLimit, err := jobs.CalculateJobMemoryLimit(memLimitInMB)
			if err != nil {
				return err
			}
			return jobs.SetJobObjMemoryLimit(containerID, jobMemLimit)
		} else if cli.IsSet("affinity") {
			affinityString := cli.String("affinity")
			affinity, err := strconv.ParseUint(affinityString, 2, 64)
			if err != nil {
				return err
			}
			return jobs.SetJobObjectAffinity(containerID, affinity)
		}
		return nil
	},
}

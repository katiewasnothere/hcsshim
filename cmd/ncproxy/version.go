package main

import (
	"fmt"

	"github.com/urfave/cli"
)

const ncproxyVersion = "1.0"

var versionCommand = cli.Command{
	Name:  "version",
	Usage: "prints the version of the ncproxy binary",
	Action: func(context *cli.Context) error {
		fmt.Println(ncproxyVersion)
		return nil
	},
}

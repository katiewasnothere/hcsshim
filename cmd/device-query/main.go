package main

import (
	"fmt"

	"github.com/Microsoft/hcsshim/internal/cfgmgr"
)

func main() {
	result, err := cfgmgr.GetDeviceIDListPresent()
	if err != nil {
		fmt.Printf("failed to get device ID list with %v", err)
	}
	fmt.Println("results: ")
	for _, id := range result {
		fmt.Printf("%s, ", id)
	}
}

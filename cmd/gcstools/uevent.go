package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Microsoft/hcsshim/internal/guest/uevent"
	"github.com/sirupsen/logrus"
)

func pollUevent(timeout uint64) error {
	ueventSock, err := uevent.NewConnection()
	if err != nil {
		return err
	}

	duration := time.Duration(timeout) * time.Second
	timeoutChan := time.After(duration)
	for {
		select {
		case <-timeoutChan:
			return fmt.Errorf("timed out after %v seconds", timeout)
		default:
			_, data, err := ueventSock.ReadMsg()
			if err != nil {
				return err
			}
			msg, err := uevent.Parse(data)
			if err != nil {
				return err
			}

			// TODO katiewasnothere: marshal to json?
			jsonData, err := json.Marshal(msg)
			if err != nil {
				return err
			}
			fmt.Println(jsonData)
		}
	}

	return nil
}

func ueventMain() {
	timeout := flag.Uint64("timeout", 30, "timeout for reading uevents in seconds, default 30 seconds")
	flag.Parse()

	if err := pollUevent(*timeout); err != nil {
		logrus.Errorf("error in poll uevent: %s", err)
		os.Exit(-1)
	}
	os.Exit(0)
}

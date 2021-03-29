package devices

import (
	"context"
	"encoding/json"
	"io"
	"io/ioutil"
	"net"
	"strings"

	"github.com/Microsoft/hcsshim/internal/guest/uevent"
	"github.com/pkg/errors"
)

var NoExecOutputErr = errors.New("failed to get any pipe output")

func getUeventOutput(ctx context.Context, l net.Listener, errChan chan<- error, results *[]uevent.Message) {
	defer close(errChan)
	c, err := l.Accept()
	if err != nil {
		errChan <- err
		return
	}

	raw, err := ioutil.ReadAll(c)
	if err != nil && err != io.EOF {
		errChan <- err
		return
	}

	lines := strings.Split(string(raw), "\n")
	for _, l := range lines {
		var msg uevent.Message
		if err := json.Unmarshal([]byte(l), &msg); err != nil {
			errChan <- err
			continue
		}
		*results = append(*results, msg)
	}

	if len(*results) == 0 {
		errChan <- NoExecOutputErr
		return
	}
}

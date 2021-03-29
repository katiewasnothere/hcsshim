package devices

import (
	"bufio"
	"io"
	"io/ioutil"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/pkg/errors"
)

// createNamedPipeListener is a helper function to create and return a pipe listener
// and it's created path.
func createNamedPipeListener() (string, net.Listener, error) {
	g, err := guid.NewV4()
	if err != nil {
		return "", nil, err
	}
	p := `\\.\pipe\` + g.String()
	l, err := winio.ListenPipe(p, nil)
	if err != nil {
		return "", nil, err
	}
	return p, l, nil
}

// readPipeOutput is a helper function that connects to a listener and reads
// the connection's output until done. resulting values with separator `separator`
// are returned in the `result` param. The `errChan` param is used to propagate an
// errors to the calling function.
func readPipeOutput(l net.Listener, errChan chan<- error, separator string, result *[]string) {
	defer close(errChan)
	c, err := l.Accept()
	if err != nil {
		errChan <- errors.Wrapf(err, "failed to accept named pipe")
		return
	}
	bytes, err := ioutil.ReadAll(c)
	if err != nil {
		errChan <- err
		return
	}

	elementsAsString := strings.TrimSuffix(string(bytes), "\n")
	elements := strings.Split(elementsAsString, separator)
	*result = append(*result, elements...)

	if len(*result) == 0 {
		errChan <- errors.Wrapf(err, "failed to get any pipe output")
		return
	}

	errChan <- nil
}

func readPipeOutput2(l net.Listener, errChan chan<- error, done <-chan bool, delim byte, result *[]string) {
	defer close(errChan)
	c, err := l.Accept()
	if err != nil {
		errChan <- errors.Wrapf(err, "failed to accept named pipe")
		return
	}
	r := bufio.NewReader(c)
	for !<-done {
		raw, err := r.ReadString(delim)
		if err != nil && err != io.EOF {
			errChan <- err
			return
		}
		line := strings.TrimSuffix(raw, string(delim))
		*result = append(*result, line)
	}

	if len(*result) == 0 {
		errChan <- errors.Wrapf(err, "failed to get any pipe output")
		return
	}

	errChan <- nil
}

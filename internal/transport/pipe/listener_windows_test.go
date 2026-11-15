//go:build windows

package pipe

import (
	"testing"
	"time"
)

func TestListenerManyConnections(t *testing.T) {
	name := `\\.\pipe\vsystemd-test-listener-many`
	l, err := listenPipe(name, pipeACL)
	if err != nil {
		t.Fatalf("listenPipe: %v", err)
	}
	defer l.Close()

	acceptErr := make(chan error, 1)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				acceptErr <- err
				return
			}
			c.Close()
		}
	}()

	for i := 0; i < 200; i++ {
		conn, err := dialPipe(name, 2*time.Second)
		if err != nil {
			select {
			case aerr := <-acceptErr:
				t.Fatalf("dial %d: %v (accept stopped: %v)", i, err, aerr)
			default:
				t.Fatalf("dial %d: %v", i, err)
			}
		}
		conn.Close()
	}
}

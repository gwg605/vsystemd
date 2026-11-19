package control

import (
	"fmt"
	"time"

	"github.com/gwg605/vsystemd/internal/scm"
)

func waitStopped(manager scm.Manager, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		status, err := manager.QueryStatus(name)
		if err != nil {
			return err
		}
		if status.State == scm.StateStopped {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %s to stop", name)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

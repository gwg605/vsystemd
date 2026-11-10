//go:build windows

package main

import (
	"fmt"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
)

func startDaemonService(executable string) error {
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to SCM: %w", err)
	}
	defer manager.Disconnect()

	service, err := manager.OpenService(daemonServiceName)
	if err != nil {
		service, err = manager.CreateService(daemonServiceName, executable, mgr.Config{
			ServiceType:  windows.SERVICE_WIN32_OWN_PROCESS,
			StartType:    windows.SERVICE_AUTO_START,
			ErrorControl: windows.SERVICE_ERROR_NORMAL,
			DisplayName:  "vsystemd",
			Description:  "vsystemd daemon",
		}, "--mode=daemon")
		if err != nil {
			return fmt.Errorf("create service %s: %w", daemonServiceName, err)
		}
	}
	defer service.Close()

	if err := service.Start(); err != nil && err != windows.ERROR_SERVICE_ALREADY_RUNNING {
		return fmt.Errorf("start service %s: %w", daemonServiceName, err)
	}
	return nil
}

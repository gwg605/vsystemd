//go:build windows

package main

import (
	"fmt"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	daemonDisplayName = "VSystemd Service Manager"
	daemonDescription = "Manages systemd-style services and timers on this computer."
)

func startDaemonService(executable string) error {
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to SCM: %w", err)
	}
	defer manager.Disconnect()

	service, err := manager.OpenService(daemonServiceName)
	created := false
	if err != nil {
		service, err = manager.CreateService(daemonServiceName, executable, mgr.Config{
			ServiceType:  windows.SERVICE_WIN32_OWN_PROCESS,
			StartType:    windows.SERVICE_AUTO_START,
			ErrorControl: windows.SERVICE_ERROR_NORMAL,
			DisplayName:  daemonDisplayName,
			Description:  daemonDescription,
		}, "--mode=daemon")
		if err != nil {
			return fmt.Errorf("create service %s: %w", daemonServiceName, err)
		}
		created = true
	}
	defer service.Close()

	if !created {
		config, err := service.Config()
		if err != nil {
			return fmt.Errorf("get service config %s: %w", daemonServiceName, err)
		}
		config.DisplayName = daemonDisplayName
		config.Description = daemonDescription
		if err := service.UpdateConfig(config); err != nil {
			return fmt.Errorf("update service config %s: %w", daemonServiceName, err)
		}
	}

	if err := service.Start(); err != nil && err != windows.ERROR_SERVICE_ALREADY_RUNNING {
		return fmt.Errorf("start service %s: %w", daemonServiceName, err)
	}
	return nil
}

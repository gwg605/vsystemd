//go:build windows

package main

import (
	"errors"
	"fmt"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/scm"
)

const (
	daemonDisplayName = "VSystemd Service Manager"
	daemonDescription = "Manages systemd-style services and timers on this computer."
)

func startDaemonService(executable string) error {
	manager := scm.New()
	cfg := scm.ServiceConfig{
		Name:        config.ManagerServiceName,
		BinaryPath:  `"` + executable + `" --mode=daemon`,
		StartType:   scm.StartTypeAutomatic,
		User:        "LocalSystem",
		DisplayName: daemonDisplayName,
		Description: daemonDescription,
	}

	_, err := manager.QueryStatus(config.ManagerServiceName)
	switch {
	case err == nil:
		if err := manager.UpdateService(cfg, nil); err != nil {
			return fmt.Errorf("update service %s: %w", config.ManagerServiceName, err)
		}
	case errors.Is(err, scm.ErrNotFound):
		if err := manager.CreateService(cfg, nil); err != nil {
			return fmt.Errorf("create service %s: %w", config.ManagerServiceName, err)
		}
	case errors.Is(err, scm.ErrNotManaged):
		return fmt.Errorf("service %s already exists but is not managed by vsystemd; remove it and retry", config.ManagerServiceName)
	default:
		return fmt.Errorf("query service %s: %w", config.ManagerServiceName, err)
	}

	if err := manager.StartService(config.ManagerServiceName); err != nil {
		return fmt.Errorf("start service %s: %w", config.ManagerServiceName, err)
	}
	return nil
}

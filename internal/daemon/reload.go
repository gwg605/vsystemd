package daemon

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/unit"
)

var errConfigurationMissing = errors.New("configuration file is missing")

func (d *Daemon) reload() error {
	slog.Info("daemon: reload started")
	desired, present, err := d.desiredServices()
	if err != nil {
		slog.Error("daemon: reload failed to build desired services", "error", err)
		return err
	}

	services, err := d.manager.ListServices()
	if err != nil {
		slog.Error("daemon: reload failed to list services", "error", err)
		return fmt.Errorf("list services: %w", err)
	}

	existing, err := d.existingUnits(services)
	if err != nil {
		slog.Error("daemon: reload failed to list units", "error", err)
		return err
	}

	for _, service := range services {
		if service.Name == config.ManagerServiceName {
			continue
		}
		desiredConfig, ok := desired[service.Name]
		if !ok {
			id := unit.ID{Name: service.Name, Type: unit.TypeService}
			if present[service.Name] {
				slog.Warn("daemon: keeping service with invalid unit", "service", service.Name)
				continue
			}
			if service.StartType != scm.StartTypeDisabled {
				d.setLoadError(id, LoadStateNotFound, errConfigurationMissing)
				slog.Warn("daemon: keeping service without unit file", "service", service.Name)
				continue
			}
			slog.Info("daemon: removing disabled service without unit file", "service", service.Name)
			if err := d.manager.StopService(service.Name); err != nil {
				slog.Error("daemon: stop service failed", "service", service.Name, "error", err)
				return fmt.Errorf("stop service %s: %w", service.Name, err)
			}
			if err := d.manager.DeleteService(service.Name); err != nil {
				slog.Error("daemon: delete service failed", "service", service.Name, "error", err)
				return fmt.Errorf("delete service %s: %w", service.Name, err)
			}
			d.forgetUnit(id)
			continue
		}
		slog.Info("daemon: updating service", "service", service.Name)
		if err := d.manager.UpdateService(desiredConfig); err != nil {
			slog.Error("daemon: update service failed", "service", service.Name, "error", err)
			return fmt.Errorf("update service %s: %w", service.Name, err)
		}
	}
	d.pruneStates(existing)
	if err := d.reloadTimers(); err != nil {
		slog.Error("daemon: reload timers failed", "error", err)
		return err
	}
	slog.Info("daemon: reload finished")
	return nil
}

func (d *Daemon) existingUnits(services []scm.ServiceConfig) (map[string]bool, error) {
	names, err := d.store.ListUnits()
	if err != nil {
		return nil, fmt.Errorf("list units: %w", err)
	}
	existing := make(map[string]bool, len(names)+len(services))
	for _, name := range names {
		id, err := unit.ParseID(name)
		if err != nil {
			continue
		}
		existing[id.String()] = true
	}
	for _, cfg := range services {
		existing[unit.ID{Name: cfg.Name, Type: unit.TypeService}.String()] = true
	}
	return existing, nil
}

func (d *Daemon) desiredServices() (map[string]scm.ServiceConfig, map[string]bool, error) {
	names, err := d.store.ListUnits()
	if err != nil {
		return nil, nil, fmt.Errorf("list units: %w", err)
	}

	services := make(map[string]scm.ServiceConfig)
	present := make(map[string]bool)
	for _, name := range names {
		id, err := unit.ParseID(name)
		if err != nil {
			slog.Warn("daemon: skip unit", "unit", name, "error", err)
			continue
		}
		if id.Type != unit.TypeService {
			continue
		}
		present[id.Name] = true

		parsed, err := d.loadUnit(id)
		if err != nil {
			d.markLoadError(id, err)
			slog.Warn("daemon: invalid unit", "unit", id, "error", err)
			continue
		}
		d.clearLoadError(id)
		for _, warning := range parsed.Warnings {
			slog.Warn("daemon: ignored unit directive", "unit", id, "warning", warning)
		}

		services[id.Name] = scm.NewServiceConfig(parsed, d.binaryPath, scm.StartTypeNoChange)
	}
	return services, present, nil
}

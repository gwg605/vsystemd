package daemon

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/unit"
)

func (d *Daemon) reload() error {
	slog.Info("daemon: reload started")
	desired, err := d.desiredServices()
	if err != nil {
		slog.Error("daemon: reload failed to build desired services", "error", err)
		return err
	}

	services, err := d.manager.ListServices()
	if err != nil {
		slog.Error("daemon: reload failed to list services", "error", err)
		return fmt.Errorf("list services: %w", err)
	}

	for _, service := range services {
		if service.Name == config.ManagerServiceName {
			continue
		}
		desiredConfig, ok := desired[service.Name]
		if !ok {
			slog.Info("daemon: removing service", "service", service.Name)
			if err := d.manager.StopService(service.Name); err != nil {
				slog.Error("daemon: stop service failed", "service", service.Name, "error", err)
				return fmt.Errorf("stop service %s: %w", service.Name, err)
			}
			if err := d.manager.DeleteService(service.Name); err != nil {
				slog.Error("daemon: delete service failed", "service", service.Name, "error", err)
				return fmt.Errorf("delete service %s: %w", service.Name, err)
			}
			continue
		}
		slog.Info("daemon: updating service", "service", service.Name)
		if err := d.manager.UpdateService(desiredConfig); err != nil {
			slog.Error("daemon: update service failed", "service", service.Name, "error", err)
			return fmt.Errorf("update service %s: %w", service.Name, err)
		}
	}
	if err := d.reloadTimers(); err != nil {
		slog.Error("daemon: reload timers failed", "error", err)
		return err
	}
	slog.Info("daemon: reload finished")
	return nil
}

func (d *Daemon) desiredServices() (map[string]scm.ServiceConfig, error) {
	names, err := d.store.ListUnits()
	if err != nil {
		return nil, fmt.Errorf("list units: %w", err)
	}

	services := make(map[string]scm.ServiceConfig)
	for _, name := range names {
		if !strings.HasSuffix(name, ".service") {
			continue
		}

		id, err := unit.ParseID(name)
		if err != nil {
			return nil, fmt.Errorf("unit %s: %w", name, err)
		}
		data, err := d.store.ReadUnit(id.String())
		if err != nil {
			return nil, fmt.Errorf("read unit %s: %w", id, err)
		}
		parsed, err := unit.Parse(id, data)
		if err != nil {
			return nil, fmt.Errorf("parse unit %s: %w", id, err)
		}
		for _, warning := range parsed.Warnings {
			slog.Warn("daemon: ignored unit directive", "unit", id, "warning", warning)
		}

		services[id.Name] = scm.ServiceConfig{
			Name:         id.Name,
			BinaryPath:   runnerCommand(d.binaryPath, id.String()),
			Dependencies: unitDependencies(parsed),
			User:         unitUser(parsed.Service.User, id.Name),
			Description:  parsed.Description,
		}
	}
	return services, nil
}

func runnerCommand(binaryPath, unitName string) string {
	return `"` + binaryPath + `" --mode=run --unit=` + unitName
}

func unitDependencies(file *unit.UnitFile) []string {
	seen := make(map[string]struct{})
	var dependencies []string
	for _, names := range [][]string{file.Requires, file.After} {
		for _, name := range names {
			if !strings.HasSuffix(name, ".service") {
				continue
			}
			dependency := strings.TrimSuffix(name, ".service")
			if _, ok := seen[dependency]; ok {
				continue
			}
			seen[dependency] = struct{}{}
			dependencies = append(dependencies, dependency)
		}
	}
	return dependencies
}

func unitUser(user, serviceName string) string {
	if user != "" {
		return user
	}
	return `NT SERVICE\` + serviceName
}

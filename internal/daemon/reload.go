package daemon

import (
	"fmt"
	"log/slog"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/svcconfig"
	"github.com/gwg605/vsystemd/internal/unit"
)

func (d *Daemon) reload() error {
	slog.Info("daemon: reload started")
	if err := d.units.rebuild(); err != nil {
		slog.Error("daemon: reload failed to rebuild units list", "error", err)
		return err
	}
	if err := d.updateServices(); err != nil {
		slog.Error("daemon: reload failed to update services", "error", err)
		return err
	}
	if err := d.syncTimers(); err != nil {
		slog.Error("daemon: reload timers failed", "error", err)
		return err
	}
	d.pruneStates(d.units.keys())
	slog.Info("daemon: reload finished")
	return nil
}

func (d *Daemon) updateServices() error {
	services, err := d.manager.ListServices()
	if err != nil {
		return fmt.Errorf("list services: %w", err)
	}
	desired := d.desiredServices()

	for _, service := range services {
		if service.Name == config.ManagerServiceName {
			continue
		}
		if cfg, ok := desired[service.Name]; ok {
			slog.Info("daemon: updating service", "service", service.Name)
			if err := d.manager.UpdateService(cfg); err != nil {
				return fmt.Errorf("update service %s: %w", service.Name, err)
			}
			continue
		}

		entry, _ := d.units.entry(unit.ID{Name: service.Name, Type: unit.TypeService})
		if entry.hasFile {
			slog.Warn("daemon: keeping service with invalid unit", "service", service.Name)
			continue
		}
		if service.StartType != scm.StartTypeDisabled {
			slog.Warn("daemon: keeping service without unit file", "service", service.Name)
			continue
		}
		slog.Info("daemon: removing disabled service without unit file", "service", service.Name)
		if err := d.manager.StopService(service.Name); err != nil {
			return fmt.Errorf("stop service %s: %w", service.Name, err)
		}
		if err := d.manager.DeleteService(service.Name); err != nil {
			return fmt.Errorf("delete service %s: %w", service.Name, err)
		}
		d.forgetUnit(unit.ID{Name: service.Name, Type: unit.TypeService})
	}
	return nil
}

func (d *Daemon) desiredServices() map[string]scm.ServiceConfig {
	services := make(map[string]scm.ServiceConfig)
	for _, entry := range d.units.all() {
		if entry.id.Type != unit.TypeService || entry.loadState != LoadStateLoaded || entry.parsed == nil {
			continue
		}
		services[entry.id.Name] = svcconfig.FromUnit(entry.parsed, d.binaryPath, scm.StartTypeNoChange)
	}
	return services
}

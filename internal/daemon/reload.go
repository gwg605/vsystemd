package daemon

import (
	"fmt"
	"log/slog"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/scm"
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

	for _, service := range services {
		if service.Name == config.ManagerServiceName {
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

		// Only remove a disabled service once it has actually stopped; a disabled
		// but running service is kept until it is stopped.
		status, err := d.manager.QueryStatus(service.Name)
		if err != nil {
			slog.Warn("daemon: query service state failed", "service", service.Name, "error", err)
			continue
		}
		if status.State != scm.StateStopped {
			slog.Warn("daemon: keeping disabled service that is not stopped", "service", service.Name, "state", status.State)
			continue
		}
slog.Info("daemon: removing disabled service without unit file", "service", service.Name)
		d.removeGroupMembership(service)
		if err := d.manager.DeleteService(service.Name); err != nil {
			slog.Warn("daemon: delete service failed", "service", service.Name, "error", err)
			continue
		}
		d.forgetUnit(unit.ID{Name: service.Name, Type: unit.TypeService})
	}
	return nil
}

// removeGroupMembership removes the service account from the group recorded in
// the unit's applied snapshot before the service is deleted. Failures are
// logged, not fatal.
func (d *Daemon) removeGroupMembership(service scm.ServiceConfig) {
	content, err := d.manager.LoadUnitContent(service.Name)
	if err != nil {
		return
	}
	parsed, err := unit.Parse(unit.ID{Name: service.Name, Type: unit.TypeService}, content)
	if err != nil || parsed.Service == nil || parsed.Service.Group == "" {
		return
	}
	if err := d.manager.RemoveAccountFromGroup(string(service.User), parsed.Service.Group); err != nil {
		slog.Warn("daemon: remove group membership failed", "service", service.Name, "group", parsed.Service.Group, "error", err)
	}
}

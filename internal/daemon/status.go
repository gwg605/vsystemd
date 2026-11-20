package daemon

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/unit"
)

func (d *Daemon) status(id unit.ID) (protocol.StatusResponse, error) {
	if id.Type == unit.TypeTimer {
		return d.timerStatus(id)
	}

	name := id.String()
	cached, _ := d.State(name)
	scmStatus, scmErr := d.manager.QueryStatus(id.Name)
	entry, inList := d.units.entry(id)

	if !inList && errors.Is(scmErr, scm.ErrNotFound) {
		d.forgetUnit(id)
		return protocol.StatusResponse{}, fmt.Errorf("unit %s not found", name)
	}

	loadState := LoadStateNotFound
	loadError := errConfigurationMissing.Error()
	description := ""
	if inList {
		loadState = entry.loadState
		loadError = entry.loadError
		if entry.parsed != nil {
			description = entry.parsed.Description
		}
	}

	state := cached
	state.Unit = name
	scmState := scm.State(0)
	if scmErr == nil {
		scmState = scmStatus.State
	}

	return protocol.StatusResponse{
		Unit:           name,
		SystemdState:   string(state.Status(scmState)),
		SCMState:       scmState,
		PID:            state.PID,
		ExitCode:       state.ExitCode,
		RestartCount:   state.RestartCount,
		LastTransition: state.LastTransition,
		LastError:      state.LastError,
		LogTail:        d.logTail(id),
		Description:    description,
		LoadState:      string(loadState),
		LoadError:      loadError,
		Enabled:        serviceEnabled(d.manager, id.Name),
	}, nil
}

func serviceEnabled(manager scm.Manager, name string) *bool {
	cfg, err := manager.GetService(name)
	if err != nil {
		return nil
	}
	enabled := cfg.StartType != scm.StartTypeDisabled && cfg.StartType != scm.StartTypeManual
	return &enabled
}

func (d *Daemon) logTail(id unit.ID) []string {
	entry, ok := d.units.entry(id)
	if !ok || entry.parsed == nil || entry.parsed.Service == nil {
		return nil
	}
	name := id.String()

	stdout, stderr, err := entry.parsed.Service.ResolveOutputs()
	if err != nil {
		slog.Warn("daemon: invalid log output config", "unit", name, "error", err)
		return nil
	}

	target := unit.StatusTarget(stdout, stderr)
	switch target.Kind {
	case unit.OutputNull:
		return nil
	case unit.OutputFile, unit.OutputAppend:
		if err := d.store.ValidatePath(target.Path); err != nil {
			slog.Warn("daemon: invalid log path", "unit", name, "path", target.Path, "error", err)
			return nil
		}
		lines, err := logwriter.ReadTail(filepath.Clean(target.Path), 10)
		if err != nil {
			slog.Warn("daemon: read log tail failed", "unit", name, "path", target.Path, "error", err)
			return nil
		}
		return lines
	default:
		path, err := logwriter.LatestFile(logwriter.ServiceLogDir(d.logsDir, id.Name))
		if err != nil {
			slog.Warn("daemon: find latest log failed", "unit", name, "error", err)
			return nil
		}
		if path == "" {
			return nil
		}
		lines, err := logwriter.ReadTail(path, 10)
		if err != nil {
			slog.Warn("daemon: read log tail failed", "unit", name, "path", path, "error", err)
			return nil
		}
		return lines
	}
}

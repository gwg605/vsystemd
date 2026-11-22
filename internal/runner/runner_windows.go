//go:build windows

package runner

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/env"
	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/storage"
	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/unit"
	"github.com/gwg605/vsystemd/internal/unitpath"
	"github.com/gwg605/vsystemd/internal/winacl"
)

type Runner struct {
	id        unit.ID
	store     storage.Storage
	logs      *logStreamer
	handler   *controlHandler
	reporter  *stateReporter
	transport transport.Transport
}

func NewRunner(name, snapshotHash string, contents scm.ContentReader, store storage.Factory, newTransport transport.Factory, cfg config.Config, startTime time.Time) (*Runner, error) {
	if store == nil {
		return nil, errors.New("storage factory is required")
	}
	if newTransport == nil {
		return nil, errors.New("transport factory is required")
	}
	if contents == nil {
		return nil, errors.New("content reader is required")
	}

	id, err := parseServiceID(name)
	if err != nil {
		return nil, err
	}

	if snapshotHash == "" {
		slog.Error("runner: missing snapshot hash", "unit", id.String())
		return nil, fmt.Errorf("missing snapshot hash for %s", id)
	}

	st, err := store()
	if err != nil {
		return nil, fmt.Errorf("open storage: %w", err)
	}
	content, err := contents.LoadUnitContent(id.Name)
	if err != nil {
		slog.Error("runner: load unit snapshot failed", "unit", id.String(), "error", err)
		return nil, fmt.Errorf("load unit snapshot %s: %w", id, err)
	}
	if got := content.Hash(); got != snapshotHash {
		slog.Error("runner: unit snapshot hash mismatch", "unit", id.String(), "expected", snapshotHash, "actual", got)
		return nil, fmt.Errorf("unit snapshot hash mismatch for %s: expected %s, got %s", id, snapshotHash, got)
	}
	parsed, err := unit.Parse(id, content)
	if err != nil {
		return nil, fmt.Errorf("parse unit %s: %w", id, err)
	}
	if parsed.Service == nil {
		return nil, fmt.Errorf("%s is not a service unit", id)
	}
	for _, warning := range parsed.Warnings {
		slog.Warn("runner: ignored unit directive", "unit", id.String(), "warning", warning)
	}

	applyServiceDefaults(parsed.Service, cfg)

	roots := unitpath.Roots{
		LogsDir:    cfg.LogsDir,
		StateDir:   cfg.StateDir,
		CacheDir:   cfg.CacheDir,
		ConfigDir:  cfg.ConfigDir,
		RuntimeDir: cfg.RunDir,
	}
	resolved, err := unitpath.ResolveService(parsed.Service, unitpath.NewValues(roots, id))
	if err != nil {
		return nil, fmt.Errorf("expand unit %s: %w", id, err)
	}
	parsed.Service.WorkingDirectory = resolved.WorkingDirectory
	parsed.Service.StandardOutput = resolved.StandardOutput
	parsed.Service.StandardError = resolved.StandardError
	parsed.Service.Environment = resolved.Environment

	logsDirs, err := resolveDirectories(st, cfg.LogsDir, parsed.Service.LogsDirectory, "LogsDirectory")
	if err != nil {
		return nil, fmt.Errorf("resolve LogsDirectory for %s: %w", id, err)
	}
	if err := ensureDirectories(logsDirs); err != nil {
		return nil, fmt.Errorf("create LogsDirectory for %s: %w", id, err)
	}
	logsDirectory := strings.Join(logsDirs, ";")

	runtimeDirs, err := resolveDirectories(st, cfg.RunDir, parsed.Service.RuntimeDirectory, "RuntimeDirectory")
	if err != nil {
		return nil, fmt.Errorf("resolve RuntimeDirectory for %s: %w", id, err)
	}
	if err := resetDirectories(runtimeDirs); err != nil {
		return nil, fmt.Errorf("create RuntimeDirectory for %s: %w", id, err)
	}

	if err := applyDirectoryACLs(parsed.Service, id, logsDirs, runtimeDirs); err != nil {
		return nil, fmt.Errorf("apply directory ACLs for %s: %w", id, err)
	}

	if err := logwriter.Cleanup(logwriter.ServiceLogDir(cfg.LogsDir, id.Name), cfg.Logs.MaxAgeDays, startTime); err != nil {
		slog.Debug("runner: cleanup service logs failed", "unit", id.String(), "error", err)
	}

	logs, err := newLogStreamer(st, id.Name, parsed.Service, cfg.LogsDir, cfg.LogRotation(), startTime)
	if err != nil {
		return nil, fmt.Errorf("create log streamer: %w", err)
	}

	defaults := env.Defaults{
		Inherit: cfg.Environment.InheritList(),
		Vars:    cfg.Environment.Vars,
	}
	restart := newRestartController(parsed.Service)
	handler := newControlHandler(parsed.Service, restart, defaults, logs, logsDirectory, runtimeDirs, snapshotHash)

	tr, err := newTransport(transport.NewPeer(transport.PeerRunner, id.String(), false), nil)
	if err != nil {
		logs.Close()
		return nil, fmt.Errorf("create transport: %w", err)
	}
	if tr == nil {
		logs.Close()
		return nil, errors.New("transport factory returned nil")
	}

	runner := &Runner{
		id:        id,
		store:     st,
		logs:      logs,
		handler:   handler,
		transport: tr,
		reporter:  newStateReporter(tr, id.String()),
	}
	runner.handler.setReporter(runner.reporter)
	slog.Info("runner: created", "unit", id.String())
	return runner, nil
}

func applyDirectoryACLs(service *unit.ServiceUnit, id unit.ID, logsDirs, runtimeDirs []string) error {
	if len(logsDirs) == 0 && len(runtimeDirs) == 0 {
		return nil
	}
	owner, err := winacl.SID(string(scm.NewAccount(service.User, id.Name)))
	if err != nil {
		return fmt.Errorf("resolve user %q: %w", service.User, err)
	}
	group := owner
	if service.Group != "" {
		group, err = winacl.SID(service.Group)
		if err != nil {
			return fmt.Errorf("resolve group %q: %w", service.Group, err)
		}
	}
	for _, dir := range logsDirs {
		if err := winacl.SetDirectoryACL(dir, service.LogsDirectoryMode, owner, group); err != nil {
			return err
		}
	}
	for _, dir := range runtimeDirs {
		if err := winacl.SetDirectoryACL(dir, service.RuntimeDirectoryMode, owner, group); err != nil {
			return err
		}
	}
	return nil
}

func parseServiceID(name string) (unit.ID, error) {
	if filepath.Ext(name) == "" {
		name += unit.TypeService.Extension()
	}
	id, err := unit.ParseID(name)
	if err != nil {
		return unit.ID{}, err
	}
	if id.Type != unit.TypeService {
		return unit.ID{}, fmt.Errorf("%s is not a service unit", id)
	}
	return id, nil
}

func applyServiceDefaults(service *unit.ServiceUnit, cfg config.Config) {
	if service.RestartSec == 0 {
		service.RestartSec = cfg.Defaults.RestartSec()
	}
	if service.TimeoutStopSec == 0 {
		service.TimeoutStopSec = cfg.Defaults.TimeoutStopSec()
	}
	if service.StartLimitBurst == 0 {
		service.StartLimitBurst = cfg.Defaults.StartLimitBurst
	}
	if service.StartLimitIntervalSec == 0 {
		service.StartLimitIntervalSec = cfg.Defaults.StartLimitIntervalSec()
	}
}

func (r *Runner) Run() error {
	stop := make(chan struct{})
	go r.heartbeat(stop)
	defer close(stop)

	return svc.Run(r.id.Name, r.handler)
}

func (r *Runner) heartbeat(stop <-chan struct{}) {
	ticker := time.NewTicker(config.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := r.reporter.Heartbeat(); err != nil {
				slog.Warn("runner: heartbeat failed", "unit", r.id.String(), "error", err)
			}
		case <-stop:
			return
		}
	}
}

func (r *Runner) Serve(requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	return r.handler.Execute(nil, requests, changes)
}

func (r *Runner) Close() error {
	slog.Info("runner: closing", "unit", r.id.String())
	if err := removeDirectories(r.handler.runtimeDirs); err != nil {
		slog.Warn("runner: remove runtime directory failed", "unit", r.id.String(), "error", err)
	}
	transportErr := r.transport.Close()
	logsErr := r.logs.Close()
	if transportErr != nil {
		slog.Warn("runner: close transport failed", "unit", r.id.String(), "error", transportErr)
		return transportErr
	}
	if logsErr != nil {
		slog.Warn("runner: close logs failed", "unit", r.id.String(), "error", logsErr)
	}
	return logsErr
}

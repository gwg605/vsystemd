# vsystemd Development Plan

---

## Phase 1: Foundation

### 1.1 Repository init
`feature/phase-1.1-init`
- `go mod init github.com/gwg605/vsystemd`
- Directory structure
- `.gitignore`
- `PROCESS.md`, `PLAN.md`, `ARCHITECTURE.md`, `TECHSPEC.md`
- GitHub Actions CI skeleton (windows-latest)

### 1.2 Binary skeletons
`feature/phase-1.2-cmd-skeletons`
- `cmd/vsystemd/main.go` — dispatch `--mode=daemon` / `--mode=run --unit=<name>`
- `cmd/systemctl/main.go` — argparse skeleton, subcommands stubbed out
- Both compile and print usage

### 1.3 `internal/unit/`
`feature/phase-1.3-unit-parser`
- Models: `UnitFile`, `ServiceUnit`, `TimerUnit`
- Parser for `.service` and `.timer` files
- Support for all v1 directives (see ARCHITECTURE.md)
- Unit tests: happy path parsing, basic error handling

### 1.4 `internal/storage/`
`feature/phase-1.4-storage`
- Interface `Storage` with custom types
- Prod implementation (filesystem, `C:\ProgramData\vsystemd\`)
- `fake/` implementation (in-memory)
- Tests for prod and fake

### 1.5 `internal/scm/`
`feature/phase-1.5-scm`
- Interface `Manager` with custom types (`ServiceConfig`, `ServiceStatus`, `StartType`)
- Windows implementation (`//go:build windows`)
- `fake/` implementation (in-memory, simulates SCM state)
- Tests: Windows tests with build tag, fake tests without

---

## Phase 2: Transport

### 2.1 `internal/transport/`
`feature/phase-2.1-transport-interface`
- Interface `Transport`, types `Message`, `MsgHandler`, opts
- Request/response matcher (match by `request_id`, timeout, concurrent requests)
- Tests with fake transport

### 2.2 `internal/transport/pipe/`
`feature/phase-2.2-transport-pipe`
- Named pipe implementation
- Pipe envelope (`from` + `msg`)
- Lazy connect, idle timeout
- Peer name → pipe name resolution
- ACL setup
- Tests (`//go:build windows`)

### 2.3 `internal/transport/fake/`
`feature/phase-2.3-transport-fake`
- In-memory fake Transport implementation
- Used by runner, daemon, and integration tests

---

## Phase 3: Runner

### 3.1 `ProcessManager`
`feature/phase-3.1-runner-process`
- ExecStartPre → ExecStart → ExecStartPost sequence
- `-` prefix handling (ignore errors)
- `WorkingDirectory=`, `Environment=`, `EnvironmentFile=`, `User=`
- Tests with fake SCM and fake storage

### 3.2 `LogStreamer`
`feature/phase-3.2-runner-log`
- Capture stdout/stderr from child process
- Write to `storage.WriteLog()`
- `StandardOutput=file:` / `StandardError=file:` support
- Tests

### 3.3 `RestartController`
`feature/phase-3.3-runner-restart`
- `Restart=no/always/on-failure` logic
- `RestartSec=` pause between restarts
- `StartLimitBurst=` / `StartLimitIntervalSec=` — N attempts per interval
- `restart_count` tracking
- Tests

### 3.4 `SCMHandler`
`feature/phase-3.4-runner-scm`
- Register runner as SCM service handler
- Handle `SERVICE_CONTROL_STOP`, `SERVICE_CONTROL_INTERROGATE`
- `SetServiceStatus` updates
- Stop sequence: ExecStop or Ctrl+C → `TimeoutStopSec=` → `TerminateProcess()`
- Tests (`//go:build windows`)

### 3.5 `StateReporter`
`feature/phase-3.5-runner-state`
- Send `state_event` to daemon on every state change
- Serve incoming pipe `vsystemd-svc-<unitname>`
- Respond to `status_request` from daemon
- Tests with fake transport

### 3.6 Runner integration
`feature/phase-3.6-runner-integration`
- Wire all runner components together
- Integration tests: start/stop, restart on failure, StartLimitBurst exhaustion

---

## Phase 4: Daemon

### 4.1 `ServiceState` + status computation
`feature/phase-4.1-daemon-state`
- `ServiceState` struct
- Status computation table (SCM state + daemon state → systemd_state)
- Tests

### 4.2 `daemon-reload`
`feature/phase-4.2-daemon-reload`
- Scan unit files, diff against SCM
- Update / delete SCM entries accordingly
- Tests with fake SCM and fake storage

### 4.3 Runner IPC handling
`feature/phase-4.3-daemon-runner-ipc`
- Handle `state_event` from runners, update `ServiceState`
- Send `status_request` to runners on daemon start
- Tests with fake transport

### 4.4 systemctl IPC handling
`feature/phase-4.4-daemon-systemctl-ipc`
- Handle `status_request`, `list_units`, `daemon_reload` from systemctl
- Respond with `status_response`, `list_units_response`, `reload_result`, `error_response`
- Tests with fake transport

### 4.5 Timer management
`feature/phase-4.5-daemon-timers`
- Handle `timer_enable`, `timer_start`, `timer_stop` from systemctl
- Persistence via `storage.WriteTimerState()` / `DeleteTimerState()`
- Auto-start `enabled=true` timers on daemon start
- Tests with fake storage and fake transport

---

## Phase 5: Binaries

### 5.1 `cmd/vsystemd/`
`feature/phase-5.1-vsystemd-cmd`
- Full `--mode=daemon` implementation (Windows Service registration, start daemon)
- Full `--mode=run` implementation (start runner for unit)

### 5.2 `cmd/systemctl/` — service commands
`feature/phase-5.2-systemctl-services`
- `enable`, `disable` — direct SCM
- `start`, `stop`, `restart` — direct SCM
- `status` — via daemon IPC
- `list-units` — via daemon IPC
- `daemon-reload` — via daemon IPC

### 5.3 `cmd/systemctl/` — timer commands
`feature/phase-5.3-systemctl-timers`
- `enable`, `disable`, `start`, `stop` for `.timer` units — via daemon IPC

---

## Phase 6: Scheduler

### 6.1 `internal/scheduler/`
`feature/phase-6.1-scheduler`
- `OnCalendar=` expression parsing via `go-systemd-time`
- Per-timer goroutine: compute next tick, wait, fire `StartService()`
- Update `last_run` / `next_run` in persistence after each tick
- Reload diff: add new, remove deleted, restart changed timers
- Tests

### 6.2 Scheduler integration into daemon
`feature/phase-6.2-scheduler-daemon`
- Wire scheduler into daemon reload and timer management
- Tests

---

## Phase 7: Integration tests

### 7.1 End-to-end scenarios
`feature/phase-7.1-integration`
- Service start/stop lifecycle
- Restart on failure, StartLimitBurst exhaustion
- daemon-reload: update, delete, add units
- Timer enable/disable/start/stop
- Daemon restart: state recovery from SCM + runner pipes

---

## Out of scope (v1)

- `OnBootSec=` / `OnStartupSec=` (Windows Task Scheduler)
- `Wants=`, `Before=`, `BindsTo=`, `PartOf=`, `Conflicts=`
- Log rotation
- Installer

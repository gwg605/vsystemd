# vsystemd Technical Specification

## Overview
### Applications and instances

vsystemd has three runtime entities: the **daemon**, **runners**, and the **control** client.

- **daemon** — the manager. A single, always-running process that owns the unit registry, computes service state, runs the timer manager, and serves the IPC endpoint. It is the source of truth for unit configuration and for daemon-specific operations.
- **runner** — a per-service process wrapper. There is one runner per managed service; it launches and supervises the service's child process, applies `Restart=`/`RestartSec=` logic, and reports state back to the daemon.
- **control** — the user-facing CLI (`systemctl.exe`). It is short-lived: it performs a single command and exits. Operations that SCM can serve go directly to SCM; daemon-specific operations (reload, unit listing, timer management) go through the daemon.

All three come from two executables. `vsystemd.exe` is dual-mode and becomes either the daemon or a runner depending on its arguments:

- the daemon starts as `vsystemd.exe --mode=daemon`; it is registered in the Windows SCM as a service and started by SCM (typically at boot);
- a runner starts as `vsystemd.exe --mode=run --unit=<name>`, where `<name>` is the unit it serves; every managed service is registered in SCM with this command line as its service binary, so SCM starts and stops the runner together with the service.

The control client is the separate `systemctl.exe`, run by the user for each command. It must be elevated: control connections to the daemon are accepted only from an elevated token.

### Interaction and data flow
### Repository structure
### v1 scope

## Interfaces
### Transport

Transport is the abstract, peer-centric IPC interface (`internal/transport`). It deals in terms of `Peer` (type + name), `Message`, `Target`, `Send`, and `Req`, and knows nothing about pipes, envelopes, or naming; the named-pipe implementation lives in `internal/transport/pipe` (see the `transport / pipe` component).

```go
type PeerType uint8 // PeerDaemon | PeerRunner | PeerControl

// Peer is a verified peer identity. It is created only by the transport;
// the application receives it in a handler or via Target.
type Peer struct{ /* typ, name, elevated */ }

func (p Peer) Type() PeerType
func (p Peer) Name() string
func (p Peer) Elevated() bool

type Message struct {
    ID        string          // UUID
    RequestID string          // UUID of the request this is a response to; "" if not a response
    Type      string          // message type
    Payload   json.RawMessage // message body
}

type MsgHandler func(source Peer, msg Message) error

type Transport interface {
    Target(typ PeerType, opts ...PeerOption) Peer
    Send(target Peer, msg Message, opts ...Opt) error
    Req(target Peer, msg Message, opts ...Opt) (Message, error)
    Close() error
}

func New(config Config, local Peer, handler MsgHandler) (Transport, error)

// Opts override config values
WithTimeout(d time.Duration)        // response timeout (for Req)
WithConnectTimeout(d time.Duration) // connect timeout (for Send and Req)

// PeerOption
WithName(name string) // target peer name (e.g. unit for a runner)
```

A handler replies using `source` as the target:

```go
func (c *Component) handleStatusRequest(source transport.Peer, msg transport.Message) error {
    resp := c.buildStatusResponse(msg)
    return c.transport.Send(source, resp)
}
```

The transport matches responses by `request_id` within a connection: `Req()` sends a message with a unique `id` and waits for a response whose `request_id` equals that `id` on the same connection; several requests may be in flight at once, and a timeout returns an error and is logged.

### SCM manager

`scm.Manager` abstracts the Windows Service Control Manager: creating, updating, deleting, starting, and stopping managed services, and querying their status. The Windows implementation lives in `internal/scm`; tests use a fake.

```go
type StartType int

// StartTypeNoChange leaves the service start type unchanged on update.
const StartTypeNoChange StartType = 0

const (
    StartTypeAutomatic StartType = iota + 1
    StartTypeManual
    StartTypeDisabled
    StartTypeBoot
    StartTypeSystem
)

type State uint32

const (
    StateStopped State = iota + 1
    StateStartPending
    StateStopPending
    StateRunning
)

// ProcessHandle is an opaque OS handle to a process (a Windows HANDLE).
type ProcessHandle uintptr

// Account is an account a service runs under.
type Account string

type ServiceConfig struct {
    Name         string
    BinaryPath   string
    StartType    StartType
    Dependencies []string
    User         Account
    password     string
    DisplayName  string
    Description  string
}

type ServiceStatus struct {
    Name     string
    State    State
    PID      uint32
    ExitCode uint32
}

type Manager interface {
    CreateService(cfg ServiceConfig) error
    UpdateService(cfg ServiceConfig) error
    // SetStartType changes only the start type of an existing managed service.
    SetStartType(name string, startType StartType) error
    DeleteService(name string) error
    StartService(name string) error
    StopService(name string) error
    QueryStatus(name string) (ServiceStatus, error)
    QueryStatuses() ([]ServiceStatus, error)
    // ListServices returns the configuration of every managed service. The
    // password is never populated because SCM does not expose it.
    ListServices() ([]ServiceConfig, error)
    // GetService returns the configuration of a single managed service.
    GetService(name string) (ServiceConfig, error)
    // GetServiceByHandle returns the configuration of the managed service the
    // process handle belongs to, or ErrNotService if it is not a service.
    GetServiceByHandle(handle ProcessHandle) (ServiceConfig, error)
}
```

`svcconfig.FromUnit(unit, binaryPath, startType)` translates a unit file into a `ServiceConfig`; `scm.NewAccount(user, name)` resolves the win32 account, defaulting to the per-service virtual account `NT SERVICE\<name>`. The password is set via `SetPassword` and is never returned by read operations (the field is unexported).

`ErrNotFound` reports a missing service, `ErrNotService` a process that is not a service, and `ErrNotManaged` a service not created by vsystemd.

On Windows, `ListServices()` returns only managed services tagged `LoadOrderGroup = "vsystemd"`; services owned by others are never touched.

### Storage

`Storage` abstracts persistence: unit files and the raw bytes of the timer database (the JSON layout is owned by `timermgr`). Logs are handled separately by `logwriter`.

```go
type Storage interface {
    // unit files
    ListUnits() ([]string, error)
    ReadUnit(name string) ([]byte, error)
    // timers (raw bytes; layout owned by timermgr)
    ReadTimers() ([]byte, error)
    WriteTimers(data []byte) error

    // ValidatePath rejects a path that is not a plain absolute file path.
    ValidatePath(path string) error
    // OutputWritable reports whether an output file's parent directory exists and is writable.
    OutputWritable(path string) error
    // InputReadable reports whether an input file can be read.
    InputReadable(path string) error
}
```

The production implementation is `internal/storage` (files under a fixed root, see `### Configuration`); tests use a fake.

### Timer manager

`timermgr.Manager` owns the timer database and the in-process `OnCalendar` scheduler. It is independent of SCM: when a timer fires it reports the event through a callback, and the caller decides what to start. The daemon drives its lifecycle (`New` → `Start` → `Stop`).

```go
// TimerConfig is the persisted configuration of a timer.
type TimerConfig struct {
    Name       string
    OnCalendar []string
}

// TimerStatus is the runtime view of a timer.
type TimerStatus struct {
    Name        string
    Enabled     bool
    Running     bool
    LastRun     time.Time
    NextRun     time.Time
    ActiveSince time.Time
}

// EventFunc is called when a timer fires.
type EventFunc func(name string, status TimerStatus)

func New(store storage.Storage, onEvent EventFunc) (*Manager, error)

// Start runs the scheduler, scheduling every enabled timer. It is idempotent.
func (m *Manager) Start() error
// Stop stops every scheduled timer and shuts the manager down. It is idempotent.
func (m *Manager) Stop() error

// SetTimer creates or updates a timer (upsert); it never changes the
// enabled or running state.
func (m *Manager) SetTimer(cfg TimerConfig) error
// DeleteTimer removes a timer; it must be both disabled and stopped.
func (m *Manager) DeleteTimer(name string) error
// Enable sets the administrative enabled flag without starting or stopping.
func (m *Manager) Enable(name string, enabled bool) error
// StartTimer puts the timer in the schedule; a no-op when already running.
func (m *Manager) StartTimer(name string) error
// StopTimer removes the timer from the schedule.
func (m *Manager) StopTimer(name string) error

func (m *Manager) QueryStatus(name string) (TimerStatus, error)
func (m *Manager) QueryStatuses() ([]TimerStatus, error)
func (m *Manager) ListTimers() ([]TimerConfig, error)
func (m *Manager) GetTimer(name string) (TimerConfig, error)
```

`New` only loads the database and never invokes the event handler; `Start` schedules the enabled timers; `Stop` stops everything and shuts down. `ErrNotFound` reports an unknown timer and `ErrClosed` an operation after `Stop`.

## Protocol

The protocol is the message layer the daemon, runners, and the control client use to talk to each other over the shared named pipe. It defines the message envelope (`id`, `request_id`, `type`, `payload`), the catalog of message types grouped by direction, and the delivery guarantees. The wire types live in `internal/protocol`, while the transport layer (`internal/transport`, `internal/transport/pipe`) carries the messages and matches responses. Runners use it to report service state to the daemon; the control client uses it for daemon-specific operations (reload, status, unit listing, timer management) that SCM cannot serve.

### Identification rules

- Every message carries a unique `id` (UUID), including responses.
- `request_id` is the `id` of the message this one is a response to; `null` if the message is not a response.
- A response has its own unique `id` and a populated `request_id`.

### Message catalog

Messages are grouped by direction.

#### runner → daemon

| Type | request_id | Fire-and-forget | Payload |
|---|---|---|---|
| `state_event` | null | yes | `unit`, `state`, `pid`, `exit_code`, `restart_count`, `timestamp`, `last_error` |

**`state_event`** — sent by a runner on every state change of the managed process (start, clean stop, crash, restart) and periodically as a heartbeat. No acknowledgement from the daemon is expected. The wire types (`StateEvent`, the message type, and the `state` values) are defined in `internal/protocol`.

#### systemctl → daemon

| Type | Expected response | Payload |
|---|---|---|
| `daemon_reload` | `reload_result` | — |
| `status_request` | `status_response` | `unit` |
| `list_units` | `list_units_response` | — |
| `timer_enable` | `timer_result` | `unit`, `enabled` (bool) |
| `timer_start` | `timer_result` | `unit` |
| `timer_stop` | `timer_result` | `unit` |

**`daemon_reload`** — triggers a rescan of unit files and a configuration update.

**`status_request`** — requests the aggregated state of a specific unit.

**`list_units`** — requests the list of all known units with their states.

**`timer_enable`** — sets `enabled=true/false` for a timer (persisted).

**`timer_start`** — puts the timer in the schedule (does not change `enabled`).

**`timer_stop`** — removes the timer from the schedule (does not change `enabled`); `next_run` and `active_since` are cleared, `last_run` is kept.

#### daemon → systemctl

| Type | request_id | Payload |
|---|---|---|
| `reload_result` | id of the `daemon_reload` request | `success` |
| `status_response` | id of the `status_request` request | `unit`, `systemd_state`, `scm_state`, `pid`, `exit_code`, `restart_count`, `last_transition`, `last_error`, `log_tail`, `description`, `enabled`; for `.timer` additionally `running`, `next_run`, `last_run`, `active_since`, `activates` |
| `list_units_response` | id of the `list_units` request | `units[]`: `unit`, `type`, `systemd_state`, `scm_state`, `description`, `enabled`, `running`, `next_run`, `last_run`, `activates` |
| `timer_result` | id of the originating request | `unit`, `success`, `enabled`, `running`, `last_run`, `next_run` |
| `error_response` | id of the originating request | `error` |

**`error_response`** — a generic response to any request that fails to process. `request_id` holds the `id` of the request that caused the error.

The `running`, `next_run`, `last_run`, `active_since`, and `activates` fields are populated only for `.timer` units; for `.service` they are `null`/empty. The `enabled` field for `.service` is derived from SCM `StartType` (`Automatic`/`Boot`/`System` → `true`, `Manual`/`Disabled` → `false`); for `.timer` it comes from the timer database.

For `.timer`, `systemd_state` is `active (waiting)` when the timer is in the schedule, otherwise `inactive (dead)`. `active_since` holds the moment the timer was put in the schedule and is cleared on `stop`.

### Delivery guarantees

The scheme is best-effort: there is no delivery acknowledgement. A lost message (pipe unavailable, timeout) is recorded in the log. The current state is always available from SCM or the next heartbeat.

## Components
### daemon

The daemon is the single, always-running manager process. It owns the unit registry and the aggregated service state, serves the IPC endpoint, drives the timer manager, and performs `daemon-reload`.

Responsibilities:

- **Unit registry (`UnitsList`)** — the source of truth for unit configuration. It is built at startup and updated only by `daemon-reload`, never by reads. It merges unit files with the SCM services and the timer database; an entry that exists in SCM or the timer database but has no unit file is kept and reported as `not-found`.
- **Service state** — combines the SCM state with the state reported by runners to compute the final systemd-style status (see `### Unit status computation`).
- **IPC server** — handles the protocol messages from runners and the control client (see `## Protocol`).
- **Timer manager** — owns the `timermgr.Manager` lifecycle (`Start`/`Stop`) and keeps the timer database in sync with the unit files.
- **Log tail** — reads the last lines of a service's log file for status responses.

`daemon-reload` rescans the unit directory and reconciles everything: it rebuilds the unit registry, updates or creates SCM services for changed units, removes disabled services that no longer have a unit file, syncs the timer database, and prunes runtime state for units that are gone.

### runner

A runner is the per-service process wrapper. It is registered in SCM as the service binary and is started/stopped by SCM; it launches and supervises the service's child process, applies the restart policy, and reports state to the daemon.

#### Components

| Component | Responsibility |
|---|---|
| `controlHandler` | Registers with SCM, receives control codes (STOP/SHUTDOWN/INTERROGATE), runs the state loop, and updates `SetServiceStatus` |
| `operation` | Runs an external process (`processOperation`) or a sequence (`sequenceOperation`), waits for completion, `Cancel(Interrupt\|Kill)` |
| `RestartController` | `Restart=` / `RestartSec=` / `StartLimitBurst=` logic and the `restart_count` counter |
| `LogStreamer` | Captures the child's stdout/stderr into a `logwriter.Writer` |
| `StateReporter` | Sends `state_event` to the daemon and drives the heartbeat timer |

#### Lifecycle

On SCM start the runner runs `ExecStartPre`, then `ExecStart` (the child process), then `ExecStartPost`, and then monitors the child while it runs. When the child exits it drains (waits for all operations to finish); if `Restart=` is active and the start limit is not exhausted, it waits `RestartSec` and repeats, otherwise it exits. On SCM stop (`SERVICE_CONTROL_STOP`) it drains — running `ExecStop` or sending Ctrl+C, and killing on timeout — and exits.

#### ExecStartPre / ExecStartPost

Behavior follows systemd:

- a non-zero `ExecStartPre` exit code makes the runner `failed` and `ExecStart` is not started;
- a non-zero `ExecStartPost` exit code makes the service `failed` and the child process is stopped;
- the `-` prefix (`-/path/to/cmd`) ignores the error and continues.

#### EnvironmentFile=

The path must be **absolute** and must not contain a `..` component; otherwise it is a startup error (the `-` prefix does not override this). Missing-file behavior follows systemd:

- `EnvironmentFile=/path` — the file is required; missing → `failed`;
- `EnvironmentFile=-/path` — the file is optional; missing is ignored.

#### Service environment

Service processes do **not** inherit the runner's environment. The environment is assembled from `config.json` (the `environment` section) and the unit directives, in increasing priority:

1. `environment.inherit` — a comma-separated list of names whose values are taken from the runner's environment; when the field is absent a fixed default list of standard Windows variables is used; `"inherit": ""` inherits nothing;
2. `environment.vars` — extra variables; in values `%NAME%` is substituted from the runner's environment (`%%` → a literal `%`; an undefined name → empty; a `%` without a closing `%` → left as is plus a warning);
3. `EnvironmentFile=` (unit);
4. `Environment=` (unit) — highest priority.

`runUnit` resolves `environment` into `runner.EnvironmentDefaults` and passes it to the runner; the runner itself stays config-agnostic.

#### Restart policy

| Directive | Behavior |
|---|---|
| `Restart=no` | Never restart |
| `Restart=always` | Always restart |
| `Restart=on-failure` | Restart only on a non-zero exit code |

- `StartLimitBurst=` / `StartLimitIntervalSec=` — as in systemd: N attempts within the interval, then `failed`;
- between attempts there is a `RestartSec=` pause.

#### Stopping the process

On `SERVICE_CONTROL_STOP`:

1. if `ExecStop=` is set, run it;
2. otherwise send `Ctrl+C` (`GenerateConsoleCtrlEvent`);
3. wait `TimeoutStopSec=` (default 45s);
4. if the process has not exited, `TerminateProcess()`.

`TimeoutStopSec=` is a unit-file directive supported in v1.

### timermgr

`timermgr` owns the timer database and runs the in-process `OnCalendar` scheduler. It is independent of SCM: when a timer fires it reports the event through a callback, and the daemon resolves the target service from the timer unit and starts it via SCM. See `### Timer manager` for the interface and `## Protocol` for the timer messages.

#### Behavior

- Stores the set of timers (`TimerConfig{Name, OnCalendar}`) and their status (`TimerStatus{Enabled, Running, LastRun, NextRun, ActiveSince}`).
- `Enabled` is an administrative flag (in the database), `Running` is a runtime status (the timer is actually in the schedule); they are independent.
- For each timer it computes the next fire time via `CalendarEvent.Next(now)`.
- It waits until that moment, then reports the fire through the callback `func(name string, status TimerStatus)`.
- The daemon looks up the timer unit by name, resolves the target service (`Unit=`) and starts it via SCM `StartService()`.
- After each fire it immediately computes the next tick and waits again (loop).
- If `CalendarEvent.Next` returns `ok=false` (no future ticks), the timer stops and a warning is logged.

**Missed ticks:** missed ticks are not made up. If the daemon was stopped, the schedule resumes from the first future tick relative to the current time at start.

#### Lifecycle

`New` only loads the database and never invokes the event handler; `Start` schedules all enabled timers; `Stop` stops everything and shuts down. The daemon calls `Start`/`Stop`.

#### Persistence

File: `C:\ProgramData\vsystemd\timers.json`

```json
{
  "foo.timer": {
    "on_calendar": ["daily"],
    "enabled": true,
    "last_run": "2026-11-09T10:00:00Z",
    "next_run": "2026-11-09T11:00:00Z",
    "active_since": "2026-11-09T09:00:00Z"
  }
}
```

The file layout is owned by `timermgr`; `storage` only keeps the bytes. After every change (fire, `start`/`stop`, `enable`/`disable`, config change) the manager serializes the database and writes it atomically via `storage.WriteTimers()` (temp file + `rename`, so a crash mid-write never leaves a corrupt `timers.json`); on `New` it reads `storage.ReadTimers()`. `Running` is not persisted — on `Start()` all `enabled` timers are put in the schedule.

`last_run`, `next_run` and `active_since` are stored and handled only in UTC (RFC3339 with `Z`). When displayed, the time is either shown in UTC with an explicit `Z` or converted to the local timezone with the zone indicated.

#### Command semantics

| Command | Effect |
|---|---|
| `systemctl enable foo.timer` | `enabled=true` in the database; does not start the timer |
| `systemctl disable foo.timer` | `enabled=false` in the database; does not stop the timer |
| `systemctl start foo.timer` | puts the timer in the schedule (running); does not change `enabled`; sets `active_since` |
| `systemctl stop foo.timer` | removes the timer from the schedule; does not change `enabled`; clears `next_run` and `active_since` |

On `Start()` all timers with `enabled=true` are automatically put in the schedule. All timer commands (`enable`, `disable`, `start`, `stop`) go through the daemon pipe, not SCM; SCM knows nothing about timers.

#### Reload

On `daemon-reload` the daemon synchronizes the database with the units:

- new timers are added (`SetTimer`); auto-start of `enabled` timers happens only in `Start()`;
- timers whose `OnCalendar=` changed are rescheduled;
- timers without a unit file are deleted only if they are `disabled` and `stopped`; otherwise they are kept.

#### OnCalendar and timespan parsing

**Library:** `gitlab.com/allddd/go-systemd-time` v1.1.0 (BSD-2-Clause)

Used to parse:

- `OnCalendar=` — via `ParseCalendarEvent(s) → CalendarEvent` and `CalendarEvent.Next(t time.Time) → (time.Time, bool)`
- `RestartSec=`, `OnBootSec=`, `OnStartupSec=` — via `ParseTimespan(s) → time.Duration`

The library supports the full systemd calendar expression syntax: lists, ranges (`..`), steps (`/`), tilde (`~` — days from the end of the month), aliases (`daily`, `weekly`, `monthly`, `hourly`, `minutely`, `quarterly`, `semiannually`, `annually`), and a timezone suffix (IANA, UTC, offset). Pure Go without cgo, works on Windows.

Timezone support comes from embedded IANA tzdata (`import _ "time/tzdata"`), with no dependency on the OS having tzdata.

Inside the code and in persistence, time is stored and handled in UTC. `OnCalendar=` without a TZ suffix is interpreted in the machine's local timezone, with a suffix in the specified timezone. On input the time is converted to UTC; when displayed it is shown in the local timezone or with the explicitly specified timezone.

### scm

`scm` is the Windows Service Control Manager integration behind `scm.Manager` (see `### SCM manager`). The Windows implementation is `internal/scm`; `internal/scm/fake` is used in tests.

Key behaviors:

- **Managed services** — vsystemd only touches services it created. Every managed service is tagged with `LoadOrderGroup = "vsystemd"`; `ListServices`, `QueryStatuses`, and every open-by-name check filter on this tag, and a service without it is reported as `ErrNotManaged`. Services owned by others are never modified.
- **Registration** — each managed service is registered with the vsystemd binary and a per-service command line: `"<vsystemd.exe>" --mode=run --unit=<name>` (built by `svcconfig.FromUnit`). SCM therefore starts a runner as the service process.
- **Accounts** — a service runs under the unit's `User=` or, by default, the per-service virtual account `NT SERVICE\<name>` (`scm.NewAccount`).
- **Start type** — the daemon sets the Windows start type (`Automatic`/`Boot`/`System` are treated as enabled, `Manual`/`Disabled` as disabled); `StartTypeNoChange` leaves it unchanged on update.
- **State** — SCM states map to `State` (`Stopped`, `StartPending`, `StopPending`, `Running`); other SCM states (pause/continue) fall through to the raw value.
- **Runner authentication** — `GetServiceByHandle` resolves the managed service a process handle belongs to, or `ErrNotService`; the daemon uses it (together with the token account and image identity) to authenticate runner connections.

### transport / pipe

`transport/pipe` is the concrete `Transport` implementation on Windows named pipes (see `### Transport`). It adds a delivery envelope, authenticates incoming connections by OS identity, and hands the application a trusted `Peer`; outgoing targets are built with `Target`.

#### Named pipe

| Party | Role | Pipe |
|---|---|---|
| daemon | listens | `\\.\pipe\vsystemd-in` |
| runner | connects | `\\.\pipe\vsystemd-in` |
| systemctl | connects | `\\.\pipe\vsystemd-in` |

The daemon keeps `vsystemd-in` open for its whole lifetime. Each runner holds a persistent connection and reconnects on a break. The control client connects only for the duration of a command.

#### Peer naming

| PeerType | peerName | Role |
|---|---|---|
| daemon | `daemon` | server, listens on `\\.\pipe\vsystemd-in` |
| runner | `foo.service` | client |
| control | `systemctl_<pid>` | client |

#### Envelope

One message on the wire is newline-delimited JSON:

```json
{
  "from": "systemctl_1234",
  "msg": {
    "id": "uuid",
    "request_id": null,
    "type": "status_request",
    "payload": { "unit": "foo.service" }
  }
}
```

The `from` field carries the sender name (untrusted). The transport compares it with the verified `Peer` name and drops the message on mismatch.

A single message is size-limited (`transport.Config.MaxMessageSize`, 4 MiB by default): a line over the limit breaks the connection. Empty and unrecognized lines are skipped.

#### Authentication of incoming connections

The authenticator is internal (not part of the public interface) and is used by the daemon when accepting a connection (`clientAuthenticator`):

- client is a managed service: own-process, `ServiceStartName` matches the token SID, and the running image `FILE_ID` matches the `FILE_ID` of the exe from `BinaryPathName` → `PeerRunner(<service>.service)` (the peer carries its `ServiceConfig`);
- otherwise, if the token is elevated and the process is not a service → `PeerControl(systemctl_<pid>)`;
- otherwise the connection is rejected.

Common checks: `GetNamedPipeClientProcessId` → `OpenProcess`; the process creation time must precede the connection (PID-reuse protection); token → elevation. Because of the elevation check the control client (`systemctl`) must run elevated.

#### Server trust

Clients (runner/control) do not verify the server: the daemon is the only owner of the pipe (`FILE_FLAG_FIRST_PIPE_INSTANCE` + ACL + early service start), so trust is positional. Squatting the name only causes message loss/DoS (not escalation): a legitimate daemon will not start while the name is taken, and `systemctl` control operations go directly to SCM.

#### Logs

Pipe-transport logs are tagged `[clientPid->serverPid]`, e.g. `pipe: [1110->13241] sent`. `systemctl` enables diagnostic logs with `--log [none|stdout|stderr]` (default `none`).

### storage

The production `Storage` implementation is file-backed under a fixed root (`C:\ProgramData\vsystemd` by default; see `### Configuration`). The paths are derived by `storage.PathsFromRoot`:

| Data | Path |
|---|---|
| unit files | `<root>\system\` |
| logs | `<root>\logs\` |
| timer database | `<root>\timers.json` |

- `ListUnits` lists the `.service` and `.timer` files in the system directory; `ReadUnit` reads a unit file.
- `ReadTimers` / `WriteTimers` read and replace the timer database as raw bytes; `WriteTimers` writes atomically (temp file + `rename`), so a crash mid-write never leaves a corrupt file. The JSON layout is owned by `timermgr`.
- Path helpers validate custom paths referenced by units: `ValidatePath` rejects anything that is not a plain absolute file path (non-absolute, `..`/`.`, trailing dots or spaces, alternate data streams, device paths `\\.\`/`\\?\`, reserved device names); `OutputWritable` checks that an output file's parent directory exists and is writable, without creating the file; `InputReadable` checks that an input file (`EnvironmentFile=`) can be read.

`storage.New` creates the required directories on startup. Tests use `internal/storage/fake`.

### unit

`internal/unit` parses unit files and models them. A unit file is an INI-like file with sections `[Unit]`, `[Service]`, `[Timer]`; the type is taken from the file extension (`.service` / `.timer`). `Parse` returns a `UnitFile`: unknown directives are collected in `Warnings` instead of failing, while a missing required section or an invalid value is a parse error.

```go
type UnitFile struct {
    Name        ID
    Description string
    After       []string
    Requires    []string
    Wants       []string
    Service     *ServiceUnit
    Timer       *TimerUnit
    Warnings    []string
}

type ServiceUnit struct {
    ExecStart             string
    ExecStartPre          []string
    ExecStartPost         []string
    ExecStop              string
    Restart               RestartPolicy
    RestartSec            time.Duration
    TimeoutStopSec        time.Duration
    WorkingDirectory      string
    Environment           map[string]string
    EnvironmentFiles      []string
    User                  string
    StandardOutput        string
    StandardError         string
    StartLimitBurst       int
    StartLimitIntervalSec time.Duration
}

type TimerUnit struct {
    OnCalendar   []string
    OnBootSec    time.Duration
    OnStartupSec time.Duration
    Unit         string
}
```

Directives by section:

- `[Unit]`: `Description`, `After`, `Requires`, `Wants` (other ordering directives are parsed but ignored with a warning).
- `[Service]`: `ExecStart`, `ExecStartPre`, `ExecStartPost`, `ExecStop`, `Restart`, `RestartSec`, `TimeoutStopSec`, `WorkingDirectory`, `Environment`, `EnvironmentFile`, `User`, `StandardOutput`, `StandardError`, `StartLimitBurst`, `StartLimitIntervalSec`.
- `[Timer]`: `OnCalendar`, `OnBootSec`, `OnStartupSec`, `Unit`.

`UnitFile.Dependencies()` returns the service units referenced by `After=`/`Requires=`, de-duplicated in declaration order; the daemon maps them to SCM dependencies. `ServiceUnit.ResolveOutputs()` resolves `StandardOutput`/`StandardError` into output targets (`null`, `file:<abs>`, `append:<abs>`, or a managed log file).

Unit names are validated by `ParseID`: the name is the file name without the extension, up to 64 characters, restricted to `[A-Za-z0-9_-]`, with a known extension.

### systemctl

`systemctl.exe` is the user-facing CLI (`cmd/systemctl`). It parses the command line, loads `config.json`, and dispatches to a `control.Controller`: operations that SCM can serve go directly to SCM, daemon-specific ones go through the daemon pipe. It must be run elevated.

| Command | Target | Effect |
|---|---|---|
| `enable <unit>` | SCM / daemon | For a service, creates or updates the SCM service (with `-p/--password`, `--password-file`, `--password-stdin` for the account); for a timer, `enabled=true` |
| `disable <unit>` | SCM / daemon | Disables the service or timer |
| `start` / `stop` / `restart <unit>` | SCM / daemon | Starts, stops, or restarts the unit |
| `status <unit>` | SCM / daemon | Prints the unit status |
| `list-units` | daemon | Lists known units |
| `list-timers [--all]` | daemon | Lists timers; `--all` includes stopped and disabled ones |
| `daemon-reload` | daemon | Reloads unit files |
| `--version` | — | Prints the version |

Global `--log [none|stdout|stderr]` (default `none`) enables diagnostic logging.

#### Status output

The output is systemd-style. For `.service`: `● unit - description`, `Loaded`, `Active` (with `since ...; ... ago`), `Main PID`, and `Exit code`/`Restart`/`Error` when present. For `.timer`: `Loaded`, `Active` (with `since ...; ... ago` when the timer is active), `Trigger` (the next run time and the remaining time) and `Triggers` (the activated service).

After the block there is a blank line and the last 10 log lines: from the current/last managed file `logs\<service>\`, or from an explicitly set `file:`/`append:` file. For a `.timer` the tail of the activated service is shown. Rotated files are not mixed in. With `null` for both streams the tail is empty.

### Unit status computation

The daemon computes a unit's status from two sources: **SCM** (the authoritative source for the running/stopped fact) and the **daemon's internal state** (richer systemd semantics reported by the runner via `state_event`). On conflict, SCM always wins.

| SCM state | Daemon state | exit_code | Result |
|---|---|---|---|
| `SERVICE_RUNNING` | `active` | — | `active (running)` |
| `SERVICE_RUNNING` | `activating` | — | `activating (start)` |
| `SERVICE_RUNNING` | `restarting` | — | `activating (auto-restart)` |
| `SERVICE_RUNNING` | `deactivating` | — | `deactivating (stop)` |
| `SERVICE_RUNNING` | no data | — | `active (running)` |
| `SERVICE_STOPPED` | `inactive` | 0 | `inactive (dead)` |
| `SERVICE_STOPPED` | `failed` | ≠ 0 | `failed` |
| `SERVICE_STOPPED` | no data | — | `inactive (dead)` |
| `SERVICE_START_PENDING` | any | — | `activating (start)` |
| `SERVICE_STOP_PENDING` | any | — | `deactivating (stop)` |
| not in SCM | any | — | `inactive (dead)` |

Rules:

- **SCM wins over daemon state** for the running/stopped fact. The daemon state refines the details within that fact (for example, it distinguishes `active` from `restarting` under `SERVICE_RUNNING`).
- **`failed`** occurs only when both conditions hold: SCM reports `SERVICE_STOPPED` and the runner reported `state=failed` with a non-zero `exit_code`. While the runner is restarting the process under `Restart=`, the status is `activating (auto-restart)`, not `failed`; `failed` is reached only when the start limit (`StartLimitBurst`) is exhausted or `Restart=no`.
- **If the daemon is not running or the runner's pipe is unavailable**, `systemd_state` is not available and the status is determined from SCM alone; `failed` is not shown, only `inactive (dead)`. This is a known difference from systemd (see ARCHITECTURE.md, known difference #2).

#### `status_response` fields

| Field | Source | Description |
|---|---|---|
| `systemd_state` | daemon internal | the computed status (`active (running)`, `failed`, ...) |
| `scm_state` | SCM | the numeric SCM state code (`SERVICE_RUNNING`, `SERVICE_STOPPED`, ...) |
| `pid` | runner / SCM | main process PID; 0 when not running |
| `exit_code` | runner | exit code of the last process run |
| `restart_count` | runner | number of automatic restarts since the last start |
| `last_transition` | daemon | timestamp of the last state change |
| `last_error` | runner | last start error produced by the runner; null if there were none |
| `log_tail` | log file | last 10 lines of the current `logs\<service>\` file or an explicit `file:`/`append:` file |
| `description` | unit file | the `Description=` value from `[Unit]` |
| `enabled` | SCM `StartType` / timers.json | for `.service` from SCM; for `.timer` from `timers.json` |
| `active_since` | timer manager / timers.json | for `.timer` the moment it was put in the schedule; absent when inactive |

### Logging

Logging is handled by `internal/logwriter`; `Storage` is not involved, except for path validation.

#### Service logs

By default a service's stdout and stderr go to `C:\ProgramData\vsystemd\logs\<service>\<timestamp>.log`:

- `<service>` is the unit name without the extension (`foo.service` → `foo`);
- `<timestamp>` is the file creation time in UTC, formatted `YYYY-MM-DD_HH-MM-SS.mmm`.

Unit directives:

- `StandardOutput=file:<path>` / `StandardError=file:<path>` — truncate, no rotation;
- `StandardOutput=append:<path>` / `StandardError=append:<path>` — append, no rotation;
- `null` — the stream is discarded;
- any other value (`journal`, garbage) — startup error.

A custom path (`file:`/`append:`) must be **absolute** and a plain file path: no `..`/`.` components, no trailing dots or spaces in components, no alternate data stream (`:`), no device paths (`\\.\`, `\\?\`), and no reserved device names (`CON`, `NUL`, `COM1`, ...); the **parent directory must exist** and be a directory (writable for `file:`/`append:`), and the path must not point at a directory; otherwise it is a startup error (directories are not created automatically). The same canonical path for stdout and stderr uses one writer; `file:` and `append:` on the same path use append for both.

#### vsystemd's own logs

- daemon → `C:\ProgramData\vsystemd\logs\vsystemd\daemon_<timestamp>.log`
- runner → `C:\ProgramData\vsystemd\logs\vsystemd\<service>_<timestamp>.log`
- plain text; files are opened in append mode and remain readable;
- rotation by `logs.max_size_mb` and `logs.max_age_days`;
- on rotation a new file with a UTC timestamp is created; renames are not used;
- closed managed logs older than `max_age_days` are deleted;
- `file:`/`append:` files are neither rotated nor deleted.

### Configuration

---

## Appendix A. Legacy content (to be migrated)

---

## IPC: схема коммуникации daemon ↔ runner ↔ systemctl

### Принцип

Коммуникация построена на модели клиент–сервер поверх одного named pipe. Сервер — daemon; клиенты — runner'ы и systemctl. Клиенты инициируют соединение к pipe демона и работают по нему в обе стороны: запрос и ответ идут по одному соединению, обратный pipe не нужен.

Демон ничего не пушит: runner сам отправляет состояние при изменении и по heartbeat-таймеру; systemctl получает ответ на свой запрос в том же соединении.

### Heartbeat и жизнеспособность

Runner держит постоянное соединение и по таймеру отправляет текущее состояние (`state_event`). Если демон не получает heartbeat дольше `RunnerDeadAfter`, раннер помечается `dead`.

### Поведение при старте daemon'а

Демон не опрашивает runner'ов. После рестарта демона соединения runner'ов рвутся; runner переподключается и отправляет текущее состояние при следующем heartbeat.

---

## Структура проекта

### Пакеты

```
cmd/
  vsystemd/             — точка входа, dispatch --mode=daemon / --mode=run
  systemctl/            — CLI

internal/
  unit/                 — парсер unit-файлов, модели UnitFile, ServiceUnit, TimerUnit
  scm/                  — интерфейс Manager + Windows реализация
  transport/            — интерфейс Transport, Message, MsgHandler, Opt
  transport/pipe/       — реализация на named pipes, pipe envelope, lazy connect
  daemon/               — Daemon, ServiceState, логика daemon-reload
  timermgr/             — база timer'ов и OnCalendar= scheduler
  runner/               — Runner, управление дочерним процессом, Restart= логика
  storage/              — интерфейс Storage + prod реализация + fake реализация

tests/                  — интеграционные тесты (fake SCM + fake Storage)
```

### Go module

- Module: `github.com/gwg605/vsystemd`
- Минимальная версия Go: 1.26+

### За рамками v1

- `OnBootSec=` / `OnStartupSec=` через Windows Task Scheduler
- `Wants=`, `Before=`, `BindsTo=`, `PartOf=`, `Conflicts=`

---

## Конфигурация

### Расположение

Путь фиксирован: `C:\ProgramData\vsystemd\config.json`. Отсутствие файла → дефолты; переопределения пути нет.

Каждый бинарь (`vsystemd`, `systemctl`) читает конфиг самостоятельно при старте. Все значения имеют дефолты, конфиг переопределяет только нужные.

### Формат

```json
{
  "system_dir": "C:\\ProgramData\\vsystemd\\system",
  "logs_dir": "C:\\ProgramData\\vsystemd\\logs",
  "timers_state_file": "C:\\ProgramData\\vsystemd\\timers.json",
  "log_level": "info",
  "logs": {
    "max_size_mb": 10,
    "max_age_days": 7
  },
  "ipc": {
    "connect_timeout_ms": 2000,
    "request_timeout_ms": 10000
  },
  "defaults": {
    "restart_sec_ms": 100,
    "timeout_stop_sec_ms": 45000,
    "start_limit_burst": 5,
    "start_limit_interval_sec_ms": 10000
  }
}
```

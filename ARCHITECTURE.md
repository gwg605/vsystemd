# vsystemd Architecture

Windows implementation of systemd with minimal functionality: services and timers.
Goal: use identical systemd unit files across cross-platform projects, replacing NSSM.

---

## Overview

Two binaries:

- `vsystemd.exe` — dual-mode binary:
  - `--mode=daemon` — Windows Service (always running), manages state, reload, SCM registration, OnCalendar timer manager
  - `--mode=run --unit=foo.service` — registered in SCM as the executable for each service; manages child process lifecycle
- `systemctl.exe` — CLI compatible with systemctl commands

---

## Unit Files

Location: `C:\ProgramData\vsystemd\system\`

Supported unit types: `.service`, `.timer`

### [Service] directives (v1)

- `ExecStart=`
- `ExecStop=`
- `ExecStartPre=`, `ExecStartPost=`
- `Restart=` (always, on-failure)
- `RestartSec=`
- `TimeoutStopSec=`
- `WorkingDirectory=`
- `Environment=`, `EnvironmentFile=`
- `User=`
- `StandardOutput=`, `StandardError=` (`null`, `file:<abs>`, `append:<abs>`; custom paths must be absolute, no `..`, parent directory must exist)
- `LogsDirectory=`, `LogsDirectoryMode=`, `RuntimeDirectory=`, `RuntimeDirectoryMode=` (see "Logs and runtime directories")
- `After=`, `Requires=` (mapped to SCM dependencies)
- `StartLimitBurst=`, `StartLimitIntervalSec=`

### Logs and runtime directories

`LogsDirectory=` takes a space-separated list of directory names; each name is a
single relative component without `..`. The runner creates `<logs_dir>\<name>`
at service start and exports `LOGS_DIRECTORY` to the service (multiple paths
joined with `;`). The directories are never removed. `LogsDirectoryMode=` is
parsed but ignored with a warning (Windows uses ACLs, not Unix mode bits).

`RuntimeDirectory=` has the same list form but is rooted at `run_dir` and
exported as `RUNTIME_DIRECTORY`. The directories are recreated (wiped and
created) at service start and removed when the service finally stops or fails;
an internal `Restart=` (auto-restart inside the runner) leaves them in place.
`RuntimeDirectoryMode=` is parsed but ignored with a warning. There is no
`RuntimeDirectoryPreserve=`.

systemd-style specifiers are expanded in `StandardOutput=`, `StandardError=`,
`WorkingDirectory=` and in `Environment=` values (not in `EnvironmentFile=`):

| Specifier | Meaning | Windows value |
|---|---|---|
| `%L` | log directory root | `logs_dir` |
| `%S` | state directory root | `state_dir` |
| `%C` | cache directory root | `cache_dir` |
| `%E` | configuration directory root | `config_dir` |
| `%t` | runtime directory root | `run_dir` |
| `%h` | manager home | the runner account's home |
| `%n` | full unit name | `foo.service` |
| `%N` | unit name without suffix | `foo` |
| `%%` | literal `%` | `%` |

An unknown specifier is a start error (as in systemd); a literal `%` must be
written `%%`. In `Exec*=` command lines, `${NAME}` references are substituted
from the service environment (an undefined name is empty; `$NAME` without braces
is literal).

### [Timer] directives (v1)

- `OnCalendar=` — handled by daemon internal timer manager
- `OnBootSec=` / `OnStartupSec=` — mapped to Task Scheduler BootTrigger with delay
- `Unit=` — target service name (defaults to same name as timer)

All other timer directives (`OnUnitActiveSec=`, `OnUnitInactiveSec=`, `Persistent=`, `WakeSystem=`, etc.) are parsed but ignored with a warning in v1.

### [Unit] directives (v1)

- `Description=`
- `After=`, `Requires=`, `Wants=`

---

## Command Semantics

### `systemctl enable foo.service`

- A valid unit file is required; a missing file is an error
- The file must match the version known to the daemon: systemctl sends it to the
  daemon (`VerifyUnitFile`). A unit unknown to the daemon, with a load error, or
  differing from the daemon's copy is rejected — run `daemon-reload` first
- On success: `CreateService()` (new) or `ChangeServiceConfig()` (existing) with
  `START_TYPE = AUTO_START` and the unit's parameters
- Password: taken from `--password`, `--password-file`, or `--password-stdin` (only one source). If none is given, an empty password is passed; there is no interactive prompt

### `systemctl disable foo.service`

- If the service exists in SCM: `ChangeServiceConfig(START_TYPE = DISABLED)`
- If the service is not registered in SCM: no-op
- `disable` neither stops nor removes the service; the SCM entry is removed by
  `daemon-reload` once the unit file is gone

### `systemctl daemon-reload`

- Routed through daemon (Channel 2)
- The daemon rebuilds its in-memory unit list (the source of truth) from the
  `system\` directory, SCM services and enabled timers:
  - file present and parses → `loaded` (the last successful parse is kept as the target)
  - file present but does not parse → `bad-setting` (last successful parse kept)
  - file missing, SCM entry exists (or timer `enabled`) → `not-found`
  - no file, no SCM entry, not an enabled timer → dropped
- It then diffs the list against SCM:
  - entry loaded, SCM entry exists → `ChangeServiceConfig()` (update)
  - entry present but not loaded, SCM entry exists → kept
  - file missing, SCM entry exists and `START_TYPE = DISABLED` → `StopService()` + `DeleteService()`
  - file missing, SCM entry exists and enabled → kept with load state `not-found`
  - unit exists, SCM entry missing → no action (wait for `enable`)
- A failure to read the `system\` directory or list SCM services aborts the reload
  with an error (the current list is kept); the daemon stays running
- Daemon reloads OnCalendar schedules from the list

### `systemctl start/stop/restart`

- Direct SCM API calls, no daemon involvement

### `systemctl status foo.service`

- Routed through daemon (Channel 2)
- Combines SCM state + daemon in-memory state + last N lines from log file
- Prints a systemd-style block: `Loaded` (path; enabled from SCM `StartType`),
  `Active` with `since`, and `Main PID`/`Exit code`/`Restart`/`Error` when relevant
- `status foo.timer` prints `Loaded`/`Active`/`Trigger`/`Triggers`; the log tail
  comes from the service the timer activates
- `Loaded:` shows the load state: `loaded` normally, or `bad-setting` / `error`
  / `not-found` (with `Reason: <message>`) when the unit file cannot be loaded

### `systemctl list-units`

- Routed through daemon (Channel 2)
- Combines SCM `EnumServicesStatus` + unit files in `system\`
- Prints `UNIT LOAD ACTIVE SUB DESCRIPTION`; `LOAD` is the load state
  (`loaded`/`bad-setting`/`error`/`not-found`) and `ACTIVE`/`SUB` are split from
  the computed state. A `●` prefix marks units that failed or have a load error

---

## IPC

Client-server over a single named pipe. The daemon owns the pipe and listens; runners and control clients connect to it. A connection is duplex, so a reply travels back on the connection the client opened — there are no per-peer inbound pipes. The daemon authenticates every inbound connection (OS identity) and hands the handler a verified `Peer`; the envelope's `from` field is untrusted and is checked against the verified peer name. The protocol is newline-delimited JSON.

| Peer | PeerType | Role |
|---|---|---|
| daemon | `daemon` | server, listens on `\\.\pipe\vsystemd-in` |
| runner (`foo.service`) | `runner` | client |
| systemctl (`systemctl_<pid>`) | `control` | client |

### Authentication

- The daemon authenticates each client: a runner is a managed service (own-process, token SID matches `ServiceStartName`, running image matches `BinaryPathName`); an elevated non-service is a control client; anything else is rejected.
- The process creation time must precede the connection (anti PID-reuse).
- The client trusts the server by position: the daemon is the only owner of the pipe (`FILE_FLAG_FIRST_PIPE_INSTANCE` + ACL + early service start). There is no client-side verification of the server.
- Consequence: `systemctl` must run elevated, otherwise the daemon rejects the control connection.

### Channel 1: runner → daemon (best-effort)

- **Pipe owner**: daemon (server)
- **Direction**: client to server (replies, if any, on the same connection)
- **Protocol**: JSON

Messages:
```
runner → daemon:  StateEvent { unit, state, pid, exit_code, restart_count, timestamp, last_error }
```

The runner keeps a persistent connection and sends a `StateEvent` on every state change and periodically as a heartbeat. The daemon never pushes and never sends a status request. The wire types (`StateEvent`, message type, state values) are defined in `internal/protocol`.

Behavior when the daemon is unavailable:
- The runner operates normally; sends fail and are logged
- The daemon marks a runner `dead` if no heartbeat arrives within the dead threshold
- After a daemon restart, runners reconnect and report state at the next heartbeat

### Channel 2: systemctl → daemon

- **Pipe owner**: daemon (server)
- **Direction**: client to server (reply on the same connection)
- **Protocol**: JSON RPC

Commands routed through daemon:
- `daemon-reload`
- `status <unit>` (aggregated state for services and timers)
- `list-units`
- `list-timers` (only active timers by default; `--all` includes stopped and disabled)
- `enable/disable/start/stop` for `.timer` units

Commands that bypass daemon (direct SCM):
- `enable`, `disable`, `start`, `stop`, `restart` for `.service` units

---

## Unit List

The daemon keeps an in-memory unit list (`UnitsList`) as the source of truth for
unit configuration. It is built at startup and updated only by `daemon-reload`;
reads (`status`, `list-units`, timer commands, runner-account checks) never touch
unit files.

- Each entry keeps the last successfully parsed unit (`parsed`), the current
  load state (`loaded`/`bad-setting`/`error`/`not-found`), the load error, and
  the file's content hash.
- File edits are not visible to the daemon until `daemon-reload` (like systemd).
  `enable` therefore requires a prior `daemon-reload`.
- `enable/disable/start/stop/restart` for `.service` units bypass the daemon and
  read SCM directly; `enable` additionally verifies the file with the daemon.

---

## State Management

Service state lives in daemon memory only; timer state is persisted separately.

```go
type RunnerState string
type SystemdState string

type ServiceState struct {
    Unit           string
    SCMState       scm.State
    RunnerState    RunnerState
    PID            uint32
    ExitCode       int
    RestartCount   int
    LastTransition time.Time
    LastError      string
}

func (s ServiceState) Status() SystemdState
```

`Status()` computes the final systemd-style state from SCM state, runner state, and exit code, for example `active (running)`, `activating (auto-restart)`, `inactive (dead)`, or `failed`.

---

## Timers

Two mechanisms depending on directive:

### OnCalendar= → daemon internal timer manager

Daemon implements a full `OnCalendar` expression parser and scheduler in-process (`internal/timermgr`).

- Full systemd calendar expression syntax supported (weekdays, ranges, lists, steps, wildcards)
- Full IANA timezone support via embedded `time/tzdata` (`import _ "time/tzdata"`)
- The manager is independent of the SCM: when a timer fires it reports the event (timer name + status) through a callback, and the daemon resolves the target service from the timer unit and calls SCM `StartService()`
- The manager loads its database on `New`, runs the scheduler on `Start` (which schedules every `enabled` timer), and shuts down on `Stop`
- The timer database (`timers.json`) holds each timer's configuration and administrative `enabled` flag; `running` is runtime-only and is re-derived on `Start`
- `systemctl stop <unit>.timer` removes the timer from the scheduler and clears its `next_run`, keeping `last_run` for history
- A timer is deleted only when it is both disabled and stopped
- Missed ticks are not caught up — if daemon was stopped, scheduling resumes from next future tick
- If daemon is down, `OnCalendar` timers do not fire (accepted limitation)

### OnBootSec= / OnStartupSec= → Windows Task Scheduler

Mapped to Task Scheduler BootTrigger with delay via COM API.

- Fires independently of daemon state
- Task Scheduler action: `sc start <service-name>`

---

## Dependencies (v1)

Only `Requires=` + `After=` are mapped natively to SCM `dwDependencies`.

All other dependency types (`Wants=`, `BindsTo=`, `PartOf=`, `Conflicts=`, `Before=`) are parsed but ignored with a warning in v1.

`After=network.target`, `After=network-online.target` — ignored (network is always available when SCM starts services).

---

## Logging

- Service stdout/stderr → `C:\ProgramData\vsystemd\logs\<service>\<timestamp>.log`
- vsystemd own logs → `C:\ProgramData\vsystemd\logs\vsystemd\daemon_<timestamp>.log` / `<service>_<timestamp>.log`
- No journald, no journalctl
- `systemctl status` shows last N lines from log file

---

## User= and Service Accounts

### Supported in v1

| Value | Notes |
|---|---|
| `NT SERVICE\<service-name>` (default if omitted) | Virtual account, auto-created by Windows |
| `LocalSystem` | |
| `NT AUTHORITY\LocalService` | |
| `NT AUTHORITY\NetworkService` | |
| `DOMAIN\user` | Password supplied via `--password`, `--password-file`, or `--password-stdin` |

There is no separate "password required" rule: if a password is supplied it is
used, otherwise an empty password is passed.

### Default behavior

If `User=` is not specified: use `NT SERVICE\<service-name>` by default (not LocalSystem).
This provides process isolation with minimal privileges.

### SCM service naming

SCM service name = unit filename without extension (`myapp.service` → SCM name `myapp`).
Virtual account name follows automatically: `NT SERVICE\myapp`.
Name collision responsibility belongs to the administrator.

### NT SERVICE\<name> permissions

The virtual account has no filesystem, registry, or network access by default.
Granting required permissions is the administrator's responsibility.

---

## Security Model (v1)

Single mechanism: ACL on all vsystemd-managed resources.

| Resource | ACL |
|---|---|
| `C:\ProgramData\vsystemd\` and `system\` | SYSTEM: Full Control, Administrators: Full Control, `NT SERVICE\All Services`: ReadAndExecute |
| `C:\ProgramData\vsystemd\logs\` | SYSTEM: Full Control, Administrators: Full Control, `NT SERVICE\All Services`: Modify |
| `\\.\pipe\vsystemd-in` | SYSTEM, Administrators, `NT SERVICE\All Services`: Full Control; owner |

No path/binary validation at enable time in v1.

---

## Known Differences from systemd

| # | Difference | Severity |
|---|---|---|
| 1 | `enable` creates SCM service (systemd does this at daemon-reload) | Low — more convenient |
| 2 | No `failed` state without daemon running | Medium |
| 3 | `Restart=` managed by runner internally, not by SCM failure actions | Low |
| 4 | `Wants=` treated as no-op in v1 | Medium |
| 5 | `After=network-online.target` ignored | Low |
| 6 | No journald / journalctl | Low |
| 7 | Requires Administrator rights for all operations | Expected for system services |
| 8 | `Before=`, `BindsTo=`, `PartOf=`, `Conflicts=` not supported in v1 | Low |
| 9 | `OnCalendar=` timers do not fire if daemon is stopped | Low — daemon is a system service |
| 10 | Missed `OnCalendar=` ticks are not caught up on daemon restart | Low |
| 11 | `LogsDirectory=` maps under `logs_dir`, never removes directories, and `LogsDirectoryMode=` is ignored (ACLs) | Low |
| 12 | `RuntimeDirectory=` removes directories only on final stop, not on internal auto-restart; no `RuntimeDirectoryPreserve=`; `RuntimeDirectoryMode=` is ignored (ACLs) | Low |

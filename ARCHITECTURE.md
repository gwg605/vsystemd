# vsystemd Architecture

Windows implementation of systemd with minimal functionality: services and timers.
Goal: use identical systemd unit files across cross-platform projects, replacing NSSM.

---

## Overview

Two binaries:

- `vsystemd.exe` — dual-mode binary:
  - `--mode=daemon` — Windows Service (always running), manages state, reload, SCM registration, OnCalendar scheduler
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
- `After=`, `Requires=` (mapped to SCM dependencies)
- `StartLimitBurst=`, `StartLimitIntervalSec=`

### [Timer] directives (v1)

- `OnCalendar=` — handled by daemon internal scheduler
- `OnBootSec=` / `OnStartupSec=` — mapped to Task Scheduler BootTrigger with delay
- `Unit=` — target service name (defaults to same name as timer)

All other timer directives (`OnUnitActiveSec=`, `OnUnitInactiveSec=`, `Persistent=`, `WakeSystem=`, etc.) are parsed but ignored with a warning in v1.

### [Unit] directives (v1)

- `Description=`
- `After=`, `Requires=`, `Wants=`

---

## Command Semantics

### `systemctl enable foo.service`

- If service does not exist in SCM: `CreateService()` with `START_TYPE = AUTO_START`
- If service already exists in SCM: `ChangeServiceConfig(START_TYPE = AUTO_START)`
- If `User=` requires a password (domain\user): request interactively or via `--password`, `--password-file`, or `--password-stdin` (only one source); attempt creation even if no password provided
- System accounts (LocalSystem, LocalService, NetworkService, `NT SERVICE\*`): no password needed

### `systemctl disable foo.service`

- If the service exists in SCM: `StopService()` (and wait for `SERVICE_STOPPED`), then `DeleteService()`
- If the service is not registered in SCM: no-op
- This is symmetric to `enable`: the SCM entry is removed

### `systemctl daemon-reload`

- Routed through daemon (Channel 2)
- Daemon scans `system\` directory, diffs against currently registered services in SCM:
  - Unit exists, SCM entry exists → `ChangeServiceConfig()` (update)
  - Unit missing, SCM entry exists → `StopService()` + `DeleteService()`
  - Unit exists, SCM entry missing → no action (wait for `enable`)
- On Windows, `ListServices()` returns only services created by vsystemd (marked with `LoadOrderGroup = "vsystemd"`), so unrelated SCM services are never modified
- Daemon reloads OnCalendar schedules from updated timer unit files

### `systemctl start/stop/restart`

- Direct SCM API calls, no daemon involvement

### `systemctl status foo.service`

- Routed through daemon (Channel 2)
- Combines SCM state + daemon in-memory state + last N lines from log file

### `systemctl list-units`

- Routed through daemon (Channel 2)
- Combines SCM `EnumServicesStatus` + unit files in `system\`

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

The runner keeps a persistent connection and sends a `StateEvent` on every state change and periodically as a heartbeat. The daemon never pushes and never sends a status request.

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

### OnCalendar= → daemon internal scheduler

Daemon implements a full `OnCalendar` expression parser and scheduler in-process.

- Full systemd calendar expression syntax supported (weekdays, ranges, lists, steps, wildcards)
- Full IANA timezone support via embedded `time/tzdata` (`import _ "time/tzdata"`)
- Scheduler fires by calling SCM `StartService()` directly
- `systemctl stop <unit>.timer` removes the timer from the scheduler and clears its `next_run`, keeping `last_run` for history
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

| Value | Password required |
|---|---|
| `NT SERVICE\<service-name>` (default if omitted) | No (virtual account, auto-created by Windows) |
| `LocalSystem` | No |
| `NT AUTHORITY\LocalService` | No |
| `NT AUTHORITY\NetworkService` | No |
| `DOMAIN\user` | Yes (prompted at enable time) |

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

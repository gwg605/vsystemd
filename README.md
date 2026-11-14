# vsystemd

`vsystemd` is a small systemd-compatible service and timer manager for Windows.
It lets you keep systemd unit files and manage services on Windows through SCM
and a `systemctl`-style CLI.

The project provides two binaries:

- `vsystemd.exe` — the daemon (`--mode=daemon`) and the per-service runner
  (`--mode=run --unit=<unit>`);
- `systemctl.exe` — CLI for enabling, starting, stopping, and inspecting units.

## Features

- `.service` and `.timer` unit files;
- `ExecStart`, `ExecStartPre`, `ExecStartPost`, `ExecStop`;
- `Restart=no|always|on-failure`, `RestartSec`, `StartLimitBurst`,
  `StartLimitIntervalSec`;
- `WorkingDirectory`, `Environment`, `EnvironmentFile`, `User`;
- service stdout/stderr logs and a daemon/runner operation log;
- Windows SCM integration with dependencies (`Requires=`, `After=`);
- timers based on systemd `OnCalendar=` expressions, with persisted
  `enabled`/`last_run`/`next_run` state;
- PowerShell setup script for default directories and ACLs.

## Requirements

- Windows 8.1 / Windows Server 2012 R2 or later;
- Windows PowerShell 4.0+ for the setup scripts;
- Administrator rights for installation and service management;
- Go 1.26+ to build from source.

## Project structure

```
cmd/
  vsystemd/          daemon and runner entrypoint
  systemctl/         CLI
internal/
  config/            config.json loading and defaults
  daemon/            daemon state, reload, IPC, timer management
  logging/           file logging setup
  protocol/          systemctl <-> daemon message types
  runner/            process management, restart logic, SCM handler, logs
  scheduler/         OnCalendar scheduler
  scm/               Windows SCM integration (+ fake/)
  storage/           unit files, logs, timer persistence (+ fake/)
  transport/         transport interface, request matching (+ fake/)
  transport/pipe/    named pipe transport
  unit/              unit file parser and models
tests/               end-to-end scenarios with fake SCM/storage/transport
examples/basic/      service/timer example and diagnostic monitor
scripts/             default path/ACL setup script
ARCHITECTURE.md
TECHSPEC.md
PLAN.md
PROCESS.md
```

## Build

From the repository root:

```powershell
go build -o bin\vsystemd.exe .\cmd\vsystemd
go build -o bin\systemctl.exe .\cmd\systemctl
```

Keep both binaries in the same directory: `systemctl` expects `vsystemd.exe`
next to itself, and `enable` registers that path in SCM.

Run tests:

```powershell
go test ./...
```

## Manual installation

All commands below must be run from an **Administrator** PowerShell or cmd.

### 1. Create default directories and permissions

```powershell
scripts\setup-paths.cmd
```

This creates:

```
C:\ProgramData\vsystemd
C:\ProgramData\vsystemd\system
C:\ProgramData\vsystemd\logs
```

and applies the ACLs required by `SYSTEM`, `Administrators`, and service
accounts (`NT SERVICE\All Services`). Use `-Check` to verify without changing:

```powershell
scripts\setup-paths.cmd -Check
```

### 2. Optional configuration

The default configuration is used when `C:\ProgramData\vsystemd\config.json`
does not exist. A custom config can be supplied via the `VSYSTEMD_CONFIG`
environment variable.

Example:

```json
{
  "system_dir": "C:\\ProgramData\\vsystemd\\system",
  "logs_dir": "C:\\ProgramData\\vsystemd\\logs",
  "timers_state_file": "C:\\ProgramData\\vsystemd\\timers.json",
  "log_file": "C:\\ProgramData\\vsystemd\\logs\\vsystemd.log",
  "log_level": "info",
  "ipc": {
    "connect_timeout_ms": 2000,
    "idle_timeout_ms": 5000,
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

### 3. Install unit files

Copy your `.service` and `.timer` files to:

```
C:\ProgramData\vsystemd\system
```

The SCM service name is the unit filename without its extension:
`foo.service` becomes the SCM service `foo`.

### 4. Start the daemon

```powershell
.\bin\vsystemd.exe --mode=daemon
```

If the `vsystemd` Windows service does not exist, this command registers it
(`LocalSystem`, automatic start) and starts it. To run the daemon in the
foreground for debugging:

```powershell
.\bin\vsystemd.exe --mode=daemon --console
```

The daemon must be running for `status`, `list-units`, `daemon-reload`, and
timer commands.

### 5. Enable and start services

`enable` reads the unit file, creates or updates the SCM service, sets its start
type to automatic, and registers `vsystemd.exe --mode=run --unit=<unit>` as its
binary:

```powershell
.\bin\systemctl.exe daemon-reload
.\bin\systemctl.exe enable foo.service
.\bin\systemctl.exe start foo.service
```

`daemon-reload` updates existing managed services and removes services whose
unit files were deleted. It does not create SCM entries for new units — run
`enable` for those.

`start`, `stop`, and `restart` operate directly on SCM. `status` and
`list-units` are served by the daemon.

### 6. Timers

```powershell
.\bin\systemctl.exe enable foo.timer
.\bin\systemctl.exe start foo.timer
```

- `enable` persists `enabled=true`; the timer is started automatically when the
  daemon starts;
- `start` adds the timer to the scheduler immediately;
- `stop` removes it from the scheduler;
- `disable` persists `enabled=false` but does not stop a running timer.

### 7. Logs and state

```
C:\ProgramData\vsystemd\logs\vsystemd.log          daemon/runner operations
C:\ProgramData\vsystemd\logs\<unit>.stdout          service stdout
C:\ProgramData\vsystemd\logs\<unit>.stderr          service stderr
C:\ProgramData\vsystemd\timers.json                 timer persistence
```

`<unit>` is the full unit filename, for example `foo.service.stdout`.

Set `"log_level": "debug"` in `config.json` for verbose daemon/runner logs.

## systemctl commands

| Command | Route | Description |
|---|---|---|
| `enable <unit>.service` | SCM | Create/update the service and set automatic start |
| `disable <unit>.service` | SCM | Set start type to disabled |
| `start/stop/restart <unit>.service` | SCM | Control the service |
| `status <unit>` | daemon | Aggregated status and log tail |
| `list-units` | daemon | List known units and states |
| `daemon-reload` | daemon | Rescan units and update SCM entries |
| `enable/disable <unit>.timer` | daemon | Persist timer enabled state |
| `start/stop <unit>.timer` | daemon | Add/remove timer from scheduler |

## Service accounts

If `User=` is omitted, vsystemd uses `NT SERVICE\<service-name>` by default.
Virtual service accounts have no filesystem access by default, so the
administrator must grant access to the directories used by the service. The
basic example uses `User=LocalSystem` to keep setup simple.

## Example

See [`examples/basic/README.md`](examples/basic/README.md) for three services
and a timer, including a diagnostic C++ monitor for console control events.

## Uninstall

```powershell
.\bin\systemctl.exe stop foo.timer
.\bin\systemctl.exe disable foo.timer
.\bin\systemctl.exe stop foo.service
sc.exe delete foo

sc.exe stop vsystemd
sc.exe delete vsystemd

Remove-Item -Recurse -Force C:\ProgramData\vsystemd
```

## Known v1 limitations

- no journald/journalctl;
- `Wants=` is parsed but ignored; `Before=`, `BindsTo=`, `PartOf=`, and
  `Conflicts=` are not supported;
- `OnBootSec=` / `OnStartupSec=` are not implemented;
- `OnCalendar=` timers run only while the daemon is running;
- missed timer ticks are not replayed after a daemon restart.

## Documentation

- [`ARCHITECTURE.md`](ARCHITECTURE.md) — components and semantics
- [`TECHSPEC.md`](TECHSPEC.md) — protocols and data formats
- [`PLAN.md`](PLAN.md) — implementation phases
- [`PROCESS.md`](PROCESS.md) — branching and review workflow
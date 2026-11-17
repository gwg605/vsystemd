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
  logging/           slog setup
  logwriter/         timestamped log files with rotation
  protocol/          systemctl <-> daemon message types
  runner/            process management, restart logic, SCM handler, logs
  scheduler/         OnCalendar scheduler
  scm/               Windows SCM integration (+ fake/)
  storage/           unit files and timer persistence (+ fake/)
  transport/         transport interface, request matching (+ fake/)
  transport/pipe/    named pipe transport
  unit/              unit file parser and models
tests/               end-to-end scenarios with fake SCM/storage/transport
VERSION              current version (embedded into the binaries)
examples/basic/      service/timer example and diagnostic monitor
scripts/             default path/ACL setup script
ARCHITECTURE.md
TECHSPEC.md
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
C:\ProgramData\vsystemd\logs\vsystemd
```

and applies the ACLs required by `SYSTEM`, `Administrators`, and service
accounts (`NT SERVICE\All Services`). Use `-Check` to verify without changing:

```powershell
scripts\setup-paths.cmd -Check
```

### 2. Optional configuration

The configuration is read from the fixed path
`C:\ProgramData\vsystemd\config.json`. When that file does not exist, built-in
defaults are used.

Example:

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
  },
  "environment": {
    "inherit": "SystemRoot,SystemDrive,windir,PATH,PATHEXT,ComSpec,TEMP,TMP",
    "vars": {
      "APP_HOME": "C:\\apps\\%COMPUTERNAME%",
      "FULL_PATH": "%PATH%;C:\\extra"
    }
  }
}
```

Service processes do **not** inherit the runner's environment. They get only
the variables listed in `environment.inherit` (a fixed default list of standard
Windows variables when the field is omitted), plus `environment.vars`, plus the
unit's `EnvironmentFile=` and `Environment=` (highest priority). In `vars`,
`%NAME%` is replaced with the runner's variable value (an undefined name is
empty; `%%` is a literal `%`; a malformed `%` is left as-is with a warning).

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
(`LocalSystem`, automatic start) and starts it.

The daemon must be running for `status`, `list-units`, `list-timers`,
`daemon-reload`, and timer commands.

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
- `stop` removes it from the scheduler and clears `next_run` while keeping
  `last_run` for history;
- `disable` persists `enabled=false` but does not stop a running timer.

Inspect timers with:

```powershell
.\bin\systemctl.exe status foo.timer
.\bin\systemctl.exe list-timers
```

Timer times are displayed in the local timezone with a numeric UTC offset.

### 7. Logs and state

`<timestamp>` is the UTC file creation time in `YYYY-MM-DD_HH-MM-SS.mmm`
format. `<service>` is the unit filename without its extension (`foo.service` →
`foo`).

```
C:\ProgramData\vsystemd\logs\vsystemd\daemon_<timestamp>.log
C:\ProgramData\vsystemd\logs\vsystemd\<service>_<timestamp>.log
C:\ProgramData\vsystemd\logs\<service>\<timestamp>.log
C:\ProgramData\vsystemd\timers.json
```

- the first file contains daemon operation logs;
- the second contains runner operation logs;
- the third contains service stdout and stderr by default;
- `StandardOutput=file:<path>` truncates the specified file;
- `StandardOutput=append:<path>` appends to the specified file;
- `StandardOutput=null` discards the stream;
- custom `file:`/`append:` paths must be absolute (no `..`), and their parent
  directory must already exist and be writable — otherwise the service fails to
  start; any other `StandardOutput`/`StandardError` value is rejected;
- `EnvironmentFile=` paths must also be absolute (no `..`); a missing file fails
  the service unless the path is prefixed with `-`;
- explicit `file:`/`append:` paths are never rotated or age-cleaned;
- managed logs are rotated when `max_size_mb` is exceeded or the file becomes
  older than `max_age_days`; a new timestamped file is started.

Log writers keep their files open but readable. `systemctl status` shows the
last lines from the current/latest managed file, or from the explicit
`file:`/`append:` target if one is configured.

Log timestamps are written in UTC with a `Z` suffix. Timer `last_run` and
`next_run` values in `timers.json` are also stored in UTC.

Set `"log_level": "debug"` in `config.json` for verbose daemon/runner logs.

## systemctl commands

| Command | Route | Description |
|---|---|---|
| `--version` | — | Print the systemctl version |
| `enable <unit>.service` | SCM | Create/update the service and set automatic start; prompts for a password for domain accounts |
| `enable <unit>.service --password-file <path>` / `--password-stdin` | SCM | Supply the service account password without putting it on the command line |
| `disable <unit>.service` | SCM | Stop and remove the service from SCM |
| `start/stop/restart <unit>.service` | SCM | Control the service |
| `status <unit>` | daemon | Aggregated status and log tail |
| `list-units` | daemon | List known units and states |
| `list-timers` | daemon | List timers with next/last run times |
| `daemon-reload` | daemon | Rescan units and update SCM entries |
| `enable/disable <unit>.timer` | daemon | Persist timer enabled state |
| `start/stop <unit>.timer` | daemon | Add/remove timer from scheduler |
| `--log [none\|stdout\|stderr]` | — | Enable diagnostic logging (default `none`) |

`systemctl` must be run elevated: the daemon rejects control connections from
non-elevated processes, and `enable`/`start`/`stop` use SCM directly. The
`--log` flag is a global option and goes after the command, e.g.
`systemctl list-units --log stderr`.

`systemctl --version` prints the version and compiled feature flags:

```
systemctl 1
+WIN32 +SCM +PIPE +TIMERS +LOGROTATE
```

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
- [`PROCESS.md`](PROCESS.md) — branching and review workflow
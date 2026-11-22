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
- `WorkingDirectory`, `Environment`, `EnvironmentFile`, `User`, `Group`;
- `LogsDirectory`/`LogsDirectoryMode`, `RuntimeDirectory`/`RuntimeDirectoryMode`
  and systemd specifiers (`%L`, `%S`, `%C`, `%E`, `%t`, `%h`, `%n`, `%N`, `%%`)
  plus `${VAR}` in `Exec*`;
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
  protocol/          IPC message types (systemctl/runner <-> daemon)
  runner/            process management, restart logic, SCM handler, logs
  timermgr/          timer database and OnCalendar scheduler
  scm/               Windows SCM integration (+ fake/)
  specifiers/        systemd-style specifier expansion (%L, %n, ...)
  storage/           unit files and raw timer persistence (+ fake/)
  transport/         transport interface, request matching (+ fake/)
  transport/pipe/    named pipe transport
  unit/              unit file parser and models
  unitpath/          shared resolver of unit paths against the specifier context
  winacl/            account SID resolution and directory ACL application
  tests/             end-to-end scenarios with fake SCM/storage/transport
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
C:\ProgramData\vsystemd\run
C:\ProgramData\vsystemd\state
C:\ProgramData\vsystemd\cache
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
  "run_dir": "C:\\ProgramData\\vsystemd\\run",
  "state_dir": "C:\\ProgramData\\vsystemd\\state",
  "cache_dir": "C:\\ProgramData\\vsystemd\\cache",
  "config_dir": "C:\\ProgramData\\vsystemd",
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
vsystemd.exe --mode=daemon
```

If the `vsystemd` Windows service does not exist, this command registers it
(`LocalSystem`, automatic start) and starts it.

The daemon must be running for `status`, `list-units`, `list-timers`,
`daemon-reload`, and timer commands.

### 5. Enable and start services

`enable` reads the unit file, asks the daemon to verify it against the daemon's
unit list, then creates or updates the SCM service, sets its start type to
automatic, writes the applied unit **snapshot** into the service's registry key,
and registers `vsystemd.exe --mode=run --unit=<unit> --snapshot=<hash>` as its
binary. A valid unit file is required, and it must match the version known to the
daemon, so run `daemon-reload` after editing a unit file:

```powershell
systemctl.exe daemon-reload
systemctl.exe enable foo.service
systemctl.exe start foo.service
```

The runner loads the unit from that snapshot (not from `system\`), so a service
always runs the configuration `enable` applied. `daemon-reload` only refreshes the
daemon's in-memory list and removes deleted, disabled services; it does **not**
change SCM configuration — `enable` is the only writer. Applying an edit therefore
takes `daemon-reload` + `enable` + `systemctl restart`.

`disable` sets the service start type to disabled; it does not stop or remove the
service. A service whose unit file was deleted is removed from SCM only once it is
both disabled **and stopped**; a disabled service that is still running is kept
until it is stopped (the daemon never stops it). Otherwise it is kept and flagged
as invalid until the unit file is restored or the service is disabled.
`daemon-reload` does not create SCM entries for new units — run `enable` for those.

The daemon keeps its own in-memory copy of the units and only refreshes it on
`daemon-reload`, so edits to unit files are not visible to `status`, `list-units`
or timer commands until then.

`start`, `stop`, and `restart` operate directly on SCM. `status` and
`list-units` are served by the daemon.

### 6. Timers

```powershell
systemctl.exe enable foo.timer
systemctl.exe start foo.timer
```

- `enable` persists `enabled=true`; the timer is started automatically when the
  daemon starts;
- `start` puts the timer in the schedule immediately;
- `stop` removes it from the schedule and clears `next_run` while keeping
  `last_run` for history;
- `disable` persists `enabled=false` but does not stop a running timer.

Inspect timers with:

```powershell
systemctl.exe status foo.timer
systemctl.exe list-timers
systemctl.exe list-timers --all
```

`list-timers` shows only timers currently in the schedule; `--all` also lists
timers that are stopped or disabled.

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
- custom `file:`/`append:` paths must be absolute and be plain file paths: no
  `..`/`.` components, no components with trailing dots or spaces, no alternate
  data streams (`:`), no device paths (`\\.\`, `\\?\`) and no reserved device
  names (`CON`, `NUL`, `COM1`, ...); the parent directory must already exist and
  be a directory (and writable for `file:`/`append:`), the target must not be a
  directory — otherwise the service fails to start; any other
  `StandardOutput`/`StandardError` value is rejected;
- `EnvironmentFile=` paths are subject to the same path rules; a missing file
  fails the service unless the path is prefixed with `-`;
- explicit `file:`/`append:` paths are never rotated or age-cleaned;
- managed logs are rotated when `max_size_mb` is exceeded or the file becomes
  older than `max_age_days`; a new timestamped file is started.

`LogsDirectory=name [name2 ...]` creates `<logs_dir>\<name>` (a single relative
component, no `..`) at service start and exports `LOGS_DIRECTORY` to the service;
several names are joined with `;` (e.g.
`LogsDirectory=app access`). The directories are kept when the service stops.

`RuntimeDirectory=name [name2 ...]` works the same way but is rooted at
`run_dir` and exported as `RUNTIME_DIRECTORY`. Unlike logs, these directories are
recreated at service start (leftovers are wiped) and removed when the service
finally stops or fails; an internal `Restart=` leaves them in place.

`LogsDirectoryMode=`/`RuntimeDirectoryMode=` (octal, default `0755`) set the
directory DACL, mapping the owner/group/other triplets to the service account
(`User=`), `Group=` (or `User=`) and `Everyone`, plus `SYSTEM`/`Administrators`
full control. `Group=` also adds the service account to that (local) group on
`enable`; the membership is removed when the service is deleted. `Group=` does
not change the process group and domain groups are not supported.

systemd specifiers are expanded in `StandardOutput=`, `StandardError=`,
`WorkingDirectory=` and in `Environment=` values: `%L` (logs), `%S` (state),
`%C` (cache), `%E` (config), `%t` (run), `%h` (home), `%n`/`%N` (unit name),
`%%` (literal `%`). The corresponding roots are `logs_dir`, `state_dir`,
`cache_dir`, `config_dir`, `run_dir` from `config.json`; the root directories are
created by `scripts\setup-paths.cmd`. An unknown specifier fails the service, so
a literal `%` must be written `%%`. In `Exec*=` command lines, `${NAME}` is
substituted from the service environment (undefined → empty; `$NAME` without
braces is literal), e.g.:

```
LogsDirectory=app
StandardOutput=append:%L\app\out.log
ExecStart=C:\app\server.exe --log-dir ${LOGS_DIRECTORY}
```

Log writers keep their files open but readable. `systemctl status` prints a
systemd-style block (`Loaded`, `Active`, `Main PID`/`Trigger`/`Triggers`) followed
by a blank line and the last lines from the current/latest managed file, or from
the explicit `file:`/`append:` target if one is configured. For a `.timer` the
tail comes from the service it activates.

When a unit's configuration cannot be loaded (unparseable file, or a missing
unit file for an enabled service), `status` reports it in the `Loaded:` line as
`bad-setting`, `error`, or `not-found` with a `Reason:`. `list-units` prints
`UNIT LOAD ACTIVE SUB DESCRIPTION` and marks failed or problem units with `●`.

If the running service loaded a snapshot that differs from the current unit file
(the file changed after `enable`), `status` adds a `Warning:` line and asks you to
`systemctl restart` the service. It appears only while the service is starting,
running, or stopping, and only once the runner has reported what it loaded.

Log timestamps are written in UTC with a `Z` suffix. Timer `last_run`, `next_run`
and `active_since` values in `timers.json` are also stored in UTC.

Set `"log_level": "debug"` in `config.json` for verbose daemon/runner logs.

## systemctl commands

| Command | Route | Description |
|---|---|---|
| `--version` | — | Print the systemctl version |
| `enable <unit>.service` | SCM+daemon | Verify the unit file against the daemon's list, then create or update the service and set automatic start; requires a prior `daemon-reload`. A service account password is taken from the flags below if given |
| `enable <unit>.service --password-file <path>` / `--password-stdin` | SCM | Supply the service account password without putting it on the command line |
| `disable <unit>.service` | SCM | Set the service start type to disabled (does not stop or remove it; `daemon-reload` removes it once the unit file is gone) |
| `start/stop/restart <unit>.service` | SCM | Control the service |
| `status <unit>` | daemon | systemd-style status block and log tail |
| `list-units` | daemon | List known units (`UNIT LOAD ACTIVE SUB DESCRIPTION`) |
| `list-timers` | daemon | List active timers with next/last run times; `--all` includes stopped and disabled timers |
| `daemon-reload` | daemon | Rescan units and update SCM entries |
| `enable/disable <unit>.timer` | daemon | Persist timer enabled state |
| `start/stop <unit>.timer` | daemon | Add/remove timer from the schedule |
| `--log [none\|stdout\|stderr]` | — | Enable diagnostic logging (default `none`) |

`systemctl` must be run elevated: the daemon rejects control connections from
non-elevated processes, and `enable`/`start`/`stop` use SCM directly. The
`--log` flag is a global option and goes after the command, e.g.
`systemctl list-units --log stderr`.

`systemctl --version` prints the version and compiled feature flags:

```
systemctl 0.2.0
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
systemctl.exe stop foo.timer
systemctl.exe disable foo.timer
systemctl.exe stop foo.service
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
- missed timer ticks are not replayed after a daemon restart;
- `LogsDirectory=` maps under `logs_dir` and its directories are not removed on
  stop;
- `RuntimeDirectory=` removes its directories only on final stop (not on an
  internal `Restart=`) and has no `RuntimeDirectoryPreserve=`;
- `…DirectoryMode=` maps to a Windows ACL (no Unix mode bits); directory
  owner/group metadata is not changed;
- `Group=` affects the directory ACL and local group membership, not the process
  primary group, and domain groups are not supported;
- a service runs the unit snapshot applied by `enable`; SCM configuration is not
  changed by `daemon-reload` (edits need `daemon-reload` + `enable` + restart).

## Documentation

- [`ARCHITECTURE.md`](ARCHITECTURE.md) — components and semantics
- [`TECHSPEC.md`](TECHSPEC.md) — protocols and data formats
- [`PROCESS.md`](PROCESS.md) — branching and review workflow
# Basic vsystemd example

This example runs three Windows services and one timer:

| Unit | Behavior |
|---|---|
| `service1.service` | Writes the current date/time to `data\file1.txt` every 10 seconds |
| `service2.service` | Depends on `service1.service`; writes to `data\file2.txt` every 15 seconds |
| `service3.service` | Writes the current date/time to `data\file3.txt` and exits |
| `service3.timer` | Starts `service3.service` every 7 seconds |
| `monitor-demo.service` | Optional diagnostic C++ app; prints process/console/control events to stdout |

`service1` and `service2` are long-running loops. `service3` is a short one-shot
script that is started by the timer.

> `User=LocalSystem` is used to keep this example simple. In production, use a
> dedicated service account and grant it access to the required directories.

All commands below are PowerShell commands and must be run from an **Administrator**
shell in the repository root.

## 1. Build

```powershell
go build -o bin\vsystemd.exe .\cmd\vsystemd
go build -o bin\systemctl.exe .\cmd\systemctl
```

Both executables must stay in the same directory: `systemctl` looks for
`vsystemd.exe` next to itself.

## 2. Install the example files

```powershell
New-Item -ItemType Directory -Force C:\vsystemd-example | Out-Null
Copy-Item .\examples\basic\* C:\vsystemd-example -Recurse -Force
New-Item -ItemType Directory -Force C:\vsystemd-example\data | Out-Null

New-Item -ItemType Directory -Force C:\ProgramData\vsystemd\system | Out-Null
Copy-Item C:\vsystemd-example\system\* C:\ProgramData\vsystemd\system\
```

The unit files use absolute paths under `C:\vsystemd-example`.

## 3. Start the vsystemd daemon

```powershell
.\bin\vsystemd.exe --mode=daemon
```

If the `vsystemd` Windows service does not exist yet, this command registers and
starts it. To run the daemon in the foreground for debugging instead:

```powershell
.\bin\vsystemd.exe --mode=daemon --console
```

## 4. Register the units in SCM

```powershell
.\bin\systemctl.exe daemon-reload

.\bin\systemctl.exe enable service1.service
.\bin\systemctl.exe enable service2.service
.\bin\systemctl.exe enable service3.service
```

`enable` creates the SCM service (or updates it if it already exists) and sets its
start type to automatic. Enable `service1.service` before `service2.service`,
because `service2` declares a dependency on `service1`.

> `service3` is also created as an automatic service, so Windows may start it once
> at boot in addition to the timer. See [Notes](#notes) for how to avoid that.

## 5. Start the long-running services

```powershell
.\bin\systemctl.exe start service1.service
.\bin\systemctl.exe start service2.service
```

## 6. Enable and start the timer

```powershell
.\bin\systemctl.exe enable service3.timer
.\bin\systemctl.exe start service3.timer
```

`enable` persists `enabled=true` (the timer will be started automatically when the
daemon starts). `start` adds the timer to the scheduler immediately.

## 7. Verify

```powershell
.\bin\systemctl.exe status service1.service
.\bin\systemctl.exe status service2.service
.\bin\systemctl.exe list-units

Start-Sleep -Seconds 15

Get-Content -Tail 5 C:\vsystemd-example\data\file1.txt
Get-Content -Tail 5 C:\vsystemd-example\data\file2.txt
Get-Content -Tail 5 C:\vsystemd-example\data\file3.txt
```

Expected result:

- `file1.txt` gets a new line about every 10 seconds.
- `file2.txt` gets a new line about every 15 seconds.
- `file3.txt` gets a new line from the timer.

Service logs are written to:

```
C:\ProgramData\vsystemd\logs\service1.service.stdout
C:\ProgramData\vsystemd\logs\service1.service.stderr
```

(the same pattern applies to `service2` and `service3`).

## 7.1 Diagnostic monitor (optional)

`monitor-demo.service` runs a small C++ application that prints timestamps and
detailed process/console diagnostics to stdout. It is useful for investigating
how `systemctl stop` delivers Ctrl-C to a console application. When run as a
service, the runner captures stdout/stderr into
`C:\ProgramData\vsystemd\logs\monitor-demo.service.stdout` and `.stderr`.

Build it:

```powershell
cd C:\vsystemd-example\monitor
cmake -S . -B build
cmake --build build --config Release
cd C:\
```

Refresh the units and register the monitor:

```powershell
Copy-Item C:\vsystemd-example\system\monitor-demo.service C:\ProgramData\vsystemd\system\

.\bin\systemctl.exe daemon-reload
.\bin\systemctl.exe enable monitor-demo.service
.\bin\systemctl.exe start monitor-demo.service
```

Stop it and inspect the captured output:

```powershell
Measure-Command { .\bin\systemctl.exe stop monitor-demo.service }
Get-Content -Tail 60 C:\ProgramData\vsystemd\logs\monitor-demo.service.stdout
```

Expected: `stop` is fast and the log contains a `CTRL_C_EVENT` entry. If the
event does not appear and the service stops only after the timeout, the log
contains the console state captured at startup.

See [monitor/README.md](monitor/README.md) for details.

## 8. Stop and remove

Stop the timer, then the services:

```powershell
.\bin\systemctl.exe stop service3.timer
.\bin\systemctl.exe disable service3.timer

.\bin\systemctl.exe stop service1.service
.\bin\systemctl.exe stop service2.service

# If the optional monitor was enabled:
.\bin\systemctl.exe stop monitor-demo.service
```

Delete the registered services:

```powershell
sc.exe delete service1
sc.exe delete service2
sc.exe delete service3
sc.exe delete monitor-demo
```

Remove the unit files and the example directory:

```powershell
Remove-Item C:\ProgramData\vsystemd\system\service1.service
Remove-Item C:\ProgramData\vsystemd\system\service2.service
Remove-Item C:\ProgramData\vsystemd\system\service3.service
Remove-Item C:\ProgramData\vsystemd\system\service3.timer
Remove-Item C:\ProgramData\vsystemd\system\monitor-demo.service
Remove-Item -Recurse -Force C:\vsystemd-example
```

Finally, stop and remove the daemon service:

```powershell
sc.exe stop vsystemd
sc.exe delete vsystemd
```

## Notes

- **Timer cadence.** `OnCalendar=*:*:00/7` fires at seconds `0, 7, 14, ..., 56`
  of every minute. The interval between `56` and the next `0` is 4 seconds, which
  is a limitation of calendar expressions that repeat within a minute.
- **Boot start of `service3`.** To keep the SCM entry but prevent `service3` from
  starting at boot, change its start type to manual after `enable`:

  ```powershell
  sc.exe config service3 start= demand
  ```

  The timer can still start a manual service.
- **Daemon is not available.** If `systemctl status` or `list-units` reports that
  the daemon is not available, check that the `vsystemd` service is running:

  ```powershell
  sc.exe query vsystemd
  ```
- **No data files.** Check the service state and logs:

  ```powershell
  .\bin\systemctl.exe status service1.service
  Get-Content -Tail 20 C:\ProgramData\vsystemd\logs\service1.service.stderr
  ```
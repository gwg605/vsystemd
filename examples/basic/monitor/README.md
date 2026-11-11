# Diagnostic monitor

A small C++ console application used to diagnose console control events under
vsystemd.

It behaves like `service1.cmd`:

- appends a timestamp to `C:\vsystemd-example\data\file1.txt` immediately and then
  every 10 seconds;
- installs a `SetConsoleCtrlHandler` handler and writes every received console
  control event (`CTRL_C_EVENT`, `CTRL_BREAK_EVENT`, `CTRL_CLOSE_EVENT`,
  `CTRL_LOGOFF_EVENT`, `CTRL_SHUTDOWN_EVENT`) to the same file;
- on Ctrl-C / Ctrl-Break it stops the main loop and exits with code 0.

All diagnostic output is written to the same file:

```
C:\vsystemd-example\data\file1.txt
```

The path is hardcoded. Each line has the form:

```
[YYYY-MM-DD HH:MM:SS.mmm] [LEVEL] message
```

## What is logged

At startup:

- process information: PID, TID, parent PID, session id, command line, working
  directory, module path;
- token information: user, SID, integrity level, token session;
- window station and desktop names;
- selected environment variables (`USERNAME`, `USERDOMAIN`, `SESSIONNAME`,
  `USERPROFILE`, `TEMP`, and all `VSYSTEMD_*` variables);
- console information: `GetConsoleWindow`, visibility, window class/title/rect,
  console modes, code pages, screen buffer info, standard handle types;
- `GetConsoleProcessList` — all processes attached to the console;
- every window owned by the process (`EnumWindows`).

During runtime:

- `[TICK]` every 10 seconds;
- `[EVENT]` for every console control event;
- `[ERROR]` for Win32 API failures, with `FormatMessageW` text;
- unhandled exceptions and `std::terminate` are also logged.

## Build

```powershell
cd C:\vsystemd-example\monitor
cmake -S . -B build
cmake --build build --config Release
```

The executable is always placed at:

```
C:\vsystemd-example\monitor\build\bin\monitor.exe
```

regardless of the CMake generator or configuration.

## Run directly

```powershell
C:\vsystemd-example\monitor\build\bin\monitor.exe
```

Press `Ctrl-C` in the console. Then inspect:

```powershell
Get-Content -Tail 40 C:\vsystemd-example\data\file1.txt
```

You should see an `[EVENT] console control event CTRL_C_EVENT` line.

## Run as a vsystemd service

Enable and start the monitor service:

```powershell
.\bin\systemctl.exe enable monitor.service
.\bin\systemctl.exe start monitor.service
```

Stop it:

```powershell
Measure-Command { .\bin\systemctl.exe stop monitor.service }
Get-Content -Tail 40 C:\vsystemd-example\data\file1.txt
```

Expected: `stop` completes quickly and the log contains
`[EVENT] console control event CTRL_C_EVENT`.

If the log contains no `CTRL_C_EVENT` and the service is stopped only after the
timeout, the diagnostic dump shows whether the process had a console at all
(`GetConsoleWindow`, `GetConsoleCP`, `GetConsoleMode`, `GetConsoleProcessList`).

> `service1` and `monitor` both append to the same `file1.txt`. Run only one of
> them at a time.
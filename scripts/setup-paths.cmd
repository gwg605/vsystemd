@echo off
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0setup-paths.ps1" %*
exit /b %ERRORLEVEL%
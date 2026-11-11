@echo off
setlocal
set "DATA_DIR=%~dp0..\data"
if not exist "%DATA_DIR%" mkdir "%DATA_DIR%"

:loop
>>"%DATA_DIR%\file1.txt" echo %date% %time%
ping -n 11 127.0.0.1 >nul
goto loop
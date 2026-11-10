@echo off
setlocal
set "DATA_DIR=%~dp0..\data"
if not exist "%DATA_DIR%" mkdir "%DATA_DIR%"

>>"%DATA_DIR%\file3.txt" echo %date% %time%
exit /b 0
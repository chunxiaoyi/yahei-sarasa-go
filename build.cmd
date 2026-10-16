@echo off
rem Runs build.ps1 without changing the PowerShell execution policy.
rem Arguments are passed through, e.g.  build.cmd -Test   or   build.cmd -Arch arm64
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0build.ps1" %*
exit /b %ERRORLEVEL%

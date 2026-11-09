@echo off
rem LibreDCM - double-click installer for Windows
rem Libre RWS (Radiology Workflow System) - developed by Patrick Mulcahy, Mulcahy.io <dev@mulcahy.io>
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0LibreDCM.ps1" install %*
pause

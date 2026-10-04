@echo off
rem RWS DICOM - double-click installer for Windows
rem RWS (Radiology Workflow System) - developed by Patrick Mulcahy, Mulcahy.io <dev@mulcahy.io>
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0rws-dicom.ps1" install %*
pause

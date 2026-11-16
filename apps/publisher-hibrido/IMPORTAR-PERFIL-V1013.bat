@echo off
cd /d "%~dp0"
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0IMPORTAR-PERFIL-V1013.ps1"
pause

@echo off
cd /d "%~dp0"
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0INICIAR-BACKEND-LAB.ps1"
if errorlevel 1 (echo Falha ao iniciar backend. & pause)

@echo off
cd /d "%~dp0"
if not exist "LoopingBR-Publisher-Hybrid-Desktop-LAB.exe" (
 echo EXE do Desktop nao encontrado. Execute GERAR-EXES-LAB.bat.
 pause
 exit /b 1
)
start "" "%~dp0LoopingBR-Publisher-Hybrid-Desktop-LAB.exe"

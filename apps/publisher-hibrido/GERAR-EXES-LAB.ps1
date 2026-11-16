$ErrorActionPreference='Stop'
$base=Split-Path -Parent $MyInvocation.MyCommand.Path
Push-Location $base
try {
  if (-not (Get-Command go -ErrorAction SilentlyContinue)) {throw 'Go 1.23+ não encontrado. https://go.dev/dl/'}
  $env:GOOS='windows';$env:GOARCH='amd64';$env:CGO_ENABLED='0'
  & go test ./...
  if ($LASTEXITCODE -ne 0) {throw 'Falha nos testes Go'}
  & go build -trimpath '-ldflags=-s -w -H=windowsgui' -o 'LoopingBR-Publisher-Hybrid-Desktop-LAB.exe' .
  if ($LASTEXITCODE -ne 0) {throw 'Falha ao compilar Desktop'}
  & go build -trimpath '-ldflags=-s -w' -o 'LoopingBR-Hybrid-Backend-LAB.exe' ./backend
  if ($LASTEXITCODE -ne 0) {throw 'Falha ao compilar backend'}
  Write-Host 'EXEs gerados sem incorporar Client Secret do Google.' -ForegroundColor Green
} finally {Pop-Location}

$ErrorActionPreference='Stop'
$base=Split-Path -Parent $MyInvocation.MyCommand.Path
$exe=Join-Path $base 'LoopingBR-Hybrid-Backend-LAB.exe'
if (-not (Test-Path $exe)) { throw 'Backend EXE não encontrado. Use GERAR-EXES-LAB.bat ou o ZIP com executáveis.' }
Write-Host 'LoopingBR v1.0.14 LAB - Backend local experimental' -ForegroundColor Cyan
Write-Host 'Google OAuth Client type: APLICATIVO DA WEB (não Desktop)' -ForegroundColor Yellow
Write-Host 'Redirect URI EXATA no Google Cloud: http://127.0.0.1:8765/oauth/google/callback'
$webID=Read-Host 'Google Web OAuth Client ID (do projeto de laboratório)'
$secretSecure=Read-Host 'Google Web Client Secret (somente neste PC, não exibido)' -AsSecureString
$ptr=[Runtime.InteropServices.Marshal]::SecureStringToBSTR($secretSecure)
try {$secret=[Runtime.InteropServices.Marshal]::PtrToStringBSTR($ptr)} finally {[Runtime.InteropServices.Marshal]::ZeroFreeBSTR($ptr)}
if ([string]::IsNullOrWhiteSpace($webID) -or [string]::IsNullOrWhiteSpace($secret)) {throw 'Client ID e Secret são obrigatórios para o backend.'}
Write-Host 'Para login Google simples, deixe a URL do túnel vazia. Para testar GitHub Actions, inicie um túnel antes.'
$tunnel=Read-Host 'URL HTTPS do túnel (opcional; exemplo: https://xxxxx.trycloudflare.com)'
try {
  $env:LOOPINGBR_WEB_CLIENT_ID=$webID.Trim()
  $env:LOOPINGBR_WEB_CLIENT_SECRET=$secret
  if (-not [string]::IsNullOrWhiteSpace($tunnel)) {$env:LOOPINGBR_PUBLIC_URL=$tunnel.Trim()} else {Remove-Item Env:\LOOPINGBR_PUBLIC_URL -ErrorAction SilentlyContinue}
  $webID=$null;$secret=$null
  Write-Host 'Backend iniciando em http://127.0.0.1:8765. Deixe esta janela aberta.' -ForegroundColor Green
  & $exe
} finally {
  Remove-Item Env:\LOOPINGBR_WEB_CLIENT_ID -ErrorAction SilentlyContinue
  Remove-Item Env:\LOOPINGBR_WEB_CLIENT_SECRET -ErrorAction SilentlyContinue
  Remove-Item Env:\LOOPINGBR_PUBLIC_URL -ErrorAction SilentlyContinue
  $webID=$null;$secret=$null
}

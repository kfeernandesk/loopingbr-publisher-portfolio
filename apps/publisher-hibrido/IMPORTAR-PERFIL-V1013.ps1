# Copia opcional dos dados locais do LAB 1.0.13 para o LAB 1.0.14.
# NAO APAGA, MOVIMENTA OU SOBRESCREVE ARQUIVOS ORIGINAIS.
$ErrorActionPreference='Stop'
Write-Host 'IMPORTAÇÃO OPCIONAL DE PERFIL — v1.0.13 PARA v1.0.14' -ForegroundColor Cyan
Write-Host 'Feche o backend e o Desktop de AMBAS as versões antes de continuar.'
Write-Host 'A cópia inclui autorizações criptografadas Windows (DPAPI), histórico local e configuração privada.'
Write-Host 'Use o mesmo usuário Windows. Arquivos não serão enviados a terceiros.'
$accept=Read-Host 'Para COPIAR os dois perfis sem modificar os originais, digite SIM'
if ($accept -cne 'SIM') { Write-Host 'Cancelado sem alterações.'; exit 0 }
if ([string]::IsNullOrWhiteSpace($env:APPDATA)) { throw 'APPDATA não encontrado.' }
$pairs=@(
  @('LoopingBRHybridV1013Backend','LoopingBRHybridV1014Backend'),
  @('LoopingBRHybridV1013Desktop','LoopingBRHybridV1014Desktop')
)
# Evita cópia parcial se algum perfil de destino já existe.
foreach($pair in $pairs){
  $source=Join-Path $env:APPDATA $pair[0]
  $target=Join-Path $env:APPDATA $pair[1]
  if (-not (Test-Path -PathType Container $source)) {throw "Perfil de origem não encontrado: $source"}
  if (Test-Path $target) {throw "Perfil v1.0.14 já existe: $target. Para preservar dados, não sobrescreveremos nenhum arquivo."}
}
$created=@()
try {
  foreach($pair in $pairs){
    $source=Join-Path $env:APPDATA $pair[0]
    $target=Join-Path $env:APPDATA $pair[1]
    # Registra a pasta ANTES da cópia, inclusive se Copy-Item falhar no meio.
    $created+= $target
    Copy-Item -LiteralPath $source -Destination $target -Recurse -ErrorAction Stop
    Write-Host "Cópia criada: $target" -ForegroundColor Green
  }
  # Regenera a chave local para que os perfis v1.0.13 e v1.0.14 não compartilhem admin.key.
  $copyKey=Join-Path (Join-Path $env:APPDATA 'LoopingBRHybridV1014Backend') 'admin.key'
  if(Test-Path -LiteralPath $copyKey){Remove-Item -LiteralPath $copyKey -Force -ErrorAction Stop}
} catch {
  foreach($createdPath in $created){
    if (Test-Path -LiteralPath $createdPath){Remove-Item -LiteralPath $createdPath -Recurse -Force -ErrorAction SilentlyContinue}
  }
  throw 'A cópia falhou. Tente novamente após conferir as permissões do Windows. Os perfis originais não foram modificados.'
}
Write-Host 'Concluído. Perfis originais v1.0.13 continuam intactos.' -ForegroundColor Green

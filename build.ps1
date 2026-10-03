# Builds dist\YaHeiSarasa.exe (GUI, no console) and dist\yahei-sarasa-cli.exe.
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
$version = (git describe --tags --always --dirty 2>$null)
if (-not $version) { $version = 'dev' }
$ld = "-s -w -X github.com/chunxiaoyi/yahei-sarasa-go/internal/app.Version=$version"
New-Item -ItemType Directory -Force dist | Out-Null
$env:CGO_ENABLED = '0'
go build -trimpath -ldflags "$ld -H=windowsgui" -o dist\YaHeiSarasa.exe .
go build -trimpath -ldflags "$ld" -o dist\yahei-sarasa-cli.exe .
Get-FileHash dist\*.exe -Algorithm SHA256 | Format-Table -AutoSize

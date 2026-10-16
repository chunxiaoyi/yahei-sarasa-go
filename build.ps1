<#
Builds the Windows binaries into dist\ :
  YaHeiSarasa.exe       GUI (no console window)
  yahei-sarasa-cli.exe  console build for the command line

  powershell -ExecutionPolicy Bypass -File .\build.ps1
  powershell -ExecutionPolicy Bypass -File .\build.ps1 -Test        # go vet + go test first
  powershell -ExecutionPolicy Bypass -File .\build.ps1 -Arch arm64  # Windows on ARM

Or simply run build.cmd, which does the first line for you.
This file is kept ASCII-only: Windows PowerShell 5.1 reads BOM-less
scripts in the ANSI code page.
#>
param(
    [ValidateSet('amd64', 'arm64')]
    [string]$Arch = 'amd64',
    [string]$Version = '',
    [switch]$Test
)

# Not 'Stop': Windows PowerShell 5.1 turns any stderr line of a native
# program into a terminating error under 'Stop'. Exit codes are checked.
$ErrorActionPreference = 'Continue'
Set-Location -LiteralPath $PSScriptRoot

function Fail([string]$Message) {
    Write-Host "ERROR: $Message" -ForegroundColor Red
    exit 1
}

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Fail 'go not found. Install Go 1.22 or newer (winget install GoLang.Go), then open a new terminal.'
}
$goVersion = "$(& go env GOVERSION)".Trim()
if ($goVersion -match '^go1\.(\d+)' -and [int]$Matches[1] -lt 22) {
    Fail "Go 1.22 or newer is required, found $goVersion."
}

if (-not $Version) {
    $Version = 'dev'
    if ((Test-Path -LiteralPath '.git') -and (Get-Command git -ErrorAction SilentlyContinue)) {
        $described = & git describe --tags --always --dirty 2>$null
        if ($LASTEXITCODE -eq 0 -and "$described".Trim()) { $Version = "$described".Trim() }
    }
}

if ($Test) {
    Write-Host '==> go vet ./...'
    & go vet ./...
    if ($LASTEXITCODE -ne 0) { Fail 'go vet failed.' }
    Write-Host '==> go test ./...'
    & go test ./...
    if ($LASTEXITCODE -ne 0) { Fail 'go test failed.' }
}

$dist = Join-Path $PSScriptRoot 'dist'
New-Item -ItemType Directory -Force -Path $dist | Out-Null
$suffix = if ($Arch -eq 'amd64') { '' } else { "-$Arch" }
$gui = Join-Path $dist "YaHeiSarasa$suffix.exe"
$cli = Join-Path $dist "yahei-sarasa-cli$suffix.exe"
$ld = "-s -w -X github.com/chunxiaoyi/yahei-sarasa-go/internal/app.Version=$Version"

# Set the target explicitly (this also lets the script cross-compile from
# PowerShell on Linux/macOS) and restore the caller's environment after.
$saved = @{ GOOS = $env:GOOS; GOARCH = $env:GOARCH; CGO_ENABLED = $env:CGO_ENABLED }
try {
    $env:GOOS = 'windows'
    $env:GOARCH = $Arch
    $env:CGO_ENABLED = '0'
    Write-Host "==> building $Version for windows/$Arch with $goVersion"
    & go build -trimpath -ldflags "$ld -H=windowsgui" -o $gui .
    if ($LASTEXITCODE -ne 0) { Fail 'building the GUI exe failed.' }
    & go build -trimpath -ldflags $ld -o $cli .
    if ($LASTEXITCODE -ne 0) { Fail 'building the CLI exe failed.' }
}
finally {
    foreach ($k in $saved.Keys) {
        if ($null -eq $saved[$k]) { Remove-Item -LiteralPath "Env:$k" -ErrorAction SilentlyContinue }
        else { Set-Item -LiteralPath "Env:$k" -Value $saved[$k] }
    }
}

Write-Host ''
foreach ($h in Get-FileHash -Algorithm SHA256 -LiteralPath $gui, $cli) {
    Write-Host ("{0}  {1}" -f $h.Hash.ToLower(), (Split-Path -Leaf $h.Path))
}
Write-Host "Done: $dist"

# claude-pet installer for Windows.
#   irm https://raw.githubusercontent.com/DanielAckroyd/claude-pet/main/install.ps1 | iex
# Downloads the latest release, checks its checksum, installs to %LOCALAPPDATA%\Programs\claude-pet
# (override with $env:CLAUDE_PET_BIN), adds that to your user PATH, then runs `claude-pet setup`.
$ErrorActionPreference = 'Stop'

$repo = 'DanielAckroyd/claude-pet'
$arch = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq 'Arm64') { 'arm64' } else { 'amd64' }
$name = "claude-pet_windows_$arch.zip"
$base = "https://github.com/$repo/releases/latest/download"
$dir = if ($env:CLAUDE_PET_BIN) { $env:CLAUDE_PET_BIN } else { Join-Path $env:LOCALAPPDATA 'Programs\claude-pet' }
$tmp = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null

try {
    Write-Host "Downloading $name"
    Invoke-WebRequest "$base/$name" -OutFile (Join-Path $tmp $name) -UseBasicParsing
    Invoke-WebRequest "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt') -UseBasicParsing

    $line = Select-String -Path (Join-Path $tmp 'checksums.txt') -Pattern " $([regex]::Escape($name))$" | Select-Object -First 1
    $want = if ($line) { $line.Line.Split(' ')[0] } else { '' }
    $got = (Get-FileHash (Join-Path $tmp $name) -Algorithm SHA256).Hash.ToLower()
    if (-not $want -or $want -ne $got) { throw "checksum mismatch for $name, not installing" }

    Expand-Archive (Join-Path $tmp $name) -DestinationPath $tmp -Force
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    Copy-Item (Join-Path $tmp 'claude-pet.exe') $dir -Force
} finally {
    Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

$exe = Join-Path $dir 'claude-pet.exe'
Write-Host "Installed $(& $exe version) to $dir"

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not (($userPath -split ';') -contains $dir)) {
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$dir", 'User')
    $env:Path += ";$dir"
    Write-Host "Added $dir to your PATH (new terminals pick it up)"
}

Write-Host ''
& $exe setup

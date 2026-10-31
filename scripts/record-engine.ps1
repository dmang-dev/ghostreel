# SPDX-License-Identifier: Apache-2.0
<#
.SYNOPSIS
Records engine ground truth (ghostreel-engine) for replays on a Windows PC
with StarCraft II installed: PLAN.md Phase 6, track B, items 2-4.

.DESCRIPTION
Each chosen replay is played back in the SC2 client of its own build, and
every unit's exact state is written every 16 loops to
<replay>.engine.pb.gz. Replays and recordings are copied to -Out, together
with each replay's map when the Battle.net cache has it, so they can be
analysed from the repository (testdata/ is not committed).

Order of work:
  1. A self-check: a 4.10 game already recorded on the Linux VM is recorded
     again here, to show the Windows path gives the same recording.
  2. Games where a Zerg side is a human player (creep speed, track B item 3).
  3. The newest other games whose client build is installed (current-version
     ground truth, item 2; their maps serve item 4).

One SC2 client runs at a time, at below-normal priority. Ctrl+C stops after
killing the client; finished recordings are kept and skipped next time.

The game must be able to write to its install folder: the client is started
with -dataDir pointing there. The script checks this first and says what to
do if it can't.

.EXAMPLE
powershell -ExecutionPolicy Bypass -File scripts\record-engine.ps1

.EXAMPLE
powershell -ExecutionPolicy Bypass -File scripts\record-engine.ps1 -Max 5 -Replays D:\replays
#>
param(
    # SC2 install folder (holding Versions\). Found from the uninstall entry if not given.
    [string]$SC2 = "",
    # Replay files or folders. Default: your Multiplayer replay folders and testdata\replays.
    [string[]]$Replays = @(),
    # Where replays, maps, recordings and the log go.
    [string]$Out = "",
    # At most this many recordings in steps 2 and 3 together.
    [int]$Max = 12,
    # Skip the self-check.
    [switch]$NoSelfCheck,
    # Only list what would be recorded.
    [switch]$DryRun
)

$ErrorActionPreference = "Stop"
$repo = Split-Path -Parent $PSScriptRoot
if (-not $Out) { $Out = Join-Path $repo "testdata\replays\engine-current" }
$bin = Join-Path $repo "bin"
$cli = Join-Path $bin "ghostreel.exe"
$engine = Join-Path $bin "ghostreel-engine.exe"

function Say([string]$msg) {
    $line = "{0:HH:mm:ss} {1}" -f (Get-Date), $msg
    Write-Host $line
    if (Test-Path $Out) { Add-Content -Path (Join-Path $Out "record.log") -Value $line -Encoding UTF8 }
}

# --- Tools: built from the repository when Go is installed, else the ones in bin\.
if (Get-Command go -ErrorAction SilentlyContinue) {
    Push-Location $repo
    try {
        & go build -o $cli ./cmd/ghostreel
        if ($LASTEXITCODE -ne 0) { throw "go build ./cmd/ghostreel failed" }
        & go build -o $engine ./cmd/ghostreel-engine
        if ($LASTEXITCODE -ne 0) { throw "go build ./cmd/ghostreel-engine failed" }
    } finally { Pop-Location }
}
foreach ($exe in @($cli, $engine)) {
    if (-not (Test-Path $exe)) { throw "$exe is missing and Go isn't installed to build it." }
}

# --- The game.
if (-not $SC2) {
    $keys = @(
        "HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\StarCraft II",
        "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\StarCraft II"
    )
    foreach ($k in $keys) {
        $loc = (Get-ItemProperty -Path $k -ErrorAction SilentlyContinue).InstallLocation
        if ($loc -and (Test-Path $loc)) { $SC2 = $loc; break }
    }
    if (-not $SC2) { $SC2 = "C:\Program Files (x86)\StarCraft II" }
}
if (-not (Test-Path (Join-Path $SC2 "Versions"))) { throw "No SC2 install at $SC2 (no Versions folder). Pass -SC2 <folder>." }
$builds = @(Get-ChildItem (Join-Path $SC2 "Versions") -Directory -Filter "Base*" |
    Where-Object { Test-Path (Join-Path $_.FullName "SC2_x64.exe") } |
    ForEach-Object { [int]$_.Name.Substring(4) } | Sort-Object)
if ($builds.Count -eq 0) { throw "No SC2_x64.exe under $SC2\Versions." }

New-Item -ItemType Directory -Force -Path $Out | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $repo "testdata\maps") | Out-Null
Say "SC2 at $SC2; client builds: $($builds -join ', ')"

# --- Replays.
if ($Replays.Count -eq 0) {
    foreach ($docs in @([Environment]::GetFolderPath("MyDocuments"), (Join-Path $HOME "OneDrive\Documents"))) {
        $Replays += @(Get-ChildItem (Join-Path $docs "StarCraft II\Accounts\*\*\Replays\Multiplayer") -Directory -ErrorAction SilentlyContinue | ForEach-Object FullName)
    }
    $Replays += Join-Path $repo "testdata\replays"
}
$files = @()
foreach ($r in $Replays) {
    if (Test-Path $r -PathType Container) {
        $files += @(Get-ChildItem $r -Filter "*.SC2Replay" -File | ForEach-Object FullName)
    } elseif (Test-Path $r) {
        $files += (Resolve-Path $r).Path
    }
}
# Recordings made here, or by the VM, are not inputs.
$files = @($files | Where-Object { $_ -notlike "$Out\*" } | Sort-Object -Unique)
Say "$($files.Count) replays found"

function Recorded([string]$path) {
    Test-Path (Join-Path $Out ([IO.Path]::GetFileNameWithoutExtension($path) + ".engine.pb.gz"))
}
function ZergHuman($info) {
    @($info.players | Where-Object { $_.human -and $_.race -eq "Zerg" }).Count -gt 0
}

# ghostreel info -json: version, players and the locally cached map of each.
$infos = @()
for ($i = 0; $i -lt $files.Count; $i += 50) {
    $chunk = $files[$i..([Math]::Min($i + 49, $files.Count - 1))]
    $infos += @(& $cli info -json @chunk 2>$null | ForEach-Object { $_ | ConvertFrom-Json })
}
$usable = @($infos | Where-Object { -not $_.error -and ($builds -contains $_.baseBuild) })
Say "$($usable.Count) can be played by an installed client"
# SC2 keeps only the builds it has needed; opening a replay once in the game
# downloads its build, after which it can be recorded here.
$missing = @($infos | Where-Object { -not $_.error -and -not ($builds -contains $_.baseBuild) } | Group-Object baseBuild | Sort-Object Count -Descending)
foreach ($g in $missing) {
    $zerg = @($g.Group | Where-Object { ZergHuman $_ }).Count
    Say ("  skipped {0} replays of build {1} ({2}), {3} with a Zerg player: open one in SC2 to install that build" -f $g.Count, $g.Name, $g.Group[0].version, $zerg)
}

$plan = @()
# 1. Self-check against the VM's recording of a 4.10 game.
$vmGame = Join-Path $repo "testdata\replays\engine\GhostProtocol_2026-10-24_03-01-25_g9of10_Defeat.SC2Replay"
if (-not $NoSelfCheck -and (Test-Path $vmGame)) {
    if ($builds -contains 75689) {
        $plan += [pscustomobject]@{ path = $vmGame; why = "self-check (VM recording exists)"; map = "" }
    } else {
        Say "Self-check skipped: client 4.10 (Base75689) isn't installed"
    }
}
# 2 and 3. Zerg players first, then the newest builds, then the newest files.
$rest = @($usable | Where-Object { $_.path -ne $vmGame } |
    Sort-Object @{ Expression = { ZergHuman $_ }; Descending = $true },
                @{ Expression = { $_.baseBuild }; Descending = $true },
                @{ Expression = { (Get-Item $_.path).LastWriteTime }; Descending = $true } |
    Select-Object -First $Max)
foreach ($x in $rest) {
    $races = ($x.players | ForEach-Object { "$($_.race)$(if (-not $_.human) { '(AI)' })" }) -join " v "
    $why = if (ZergHuman $x) { "Zerg player" } elseif ($x.baseBuild -eq $builds[-1]) { "current version" } else { "older version" }
    $plan += [pscustomobject]@{ path = $x.path; why = "$why; $($x.version); $($x.map); $races"; map = $x.mapFile }
}

Say "Plan:"
foreach ($p in $plan) {
    $state = if (Recorded $p.path) { "done" } else { "to do" }
    Say ("  [{0}] {1} - {2}" -f $state, (Split-Path $p.path -Leaf), $p.why)
}
if ($DryRun) { exit 0 }

# SC2 writes to the folder given as -dataDir; a read-only one stops it with
# "Core: access permission error".
$probe = Join-Path $SC2 "ghostreel-write-check.tmp"
try {
    Set-Content -Path $probe -Value "" -ErrorAction Stop
    Remove-Item $probe -ErrorAction SilentlyContinue
} catch {
    Write-Host ""
    Write-Host "SC2 needs write access to $SC2 for this, and this account doesn't have it."
    Write-Host "Either run this script from an administrator PowerShell, or give your account"
    Write-Host "Modify rights on the folder once (from an administrator prompt):"
    Write-Host ""
    Write-Host "    icacls `"$SC2`" /grant `"${env:USERNAME}:(OI)(CI)M`""
    Write-Host ""
    exit 1
}
if (Get-Process SC2_x64 -ErrorAction SilentlyContinue) {
    throw "StarCraft II is running. Close it first: the engine starts its own client."
}

# The engine and the client it starts inherit this priority.
(Get-Process -Id $PID).PriorityClass = [Diagnostics.ProcessPriorityClass]::BelowNormal


# --- Record, one client at a time, below normal priority.
$manifest = Join-Path $Out "manifest.jsonl"
foreach ($p in $plan) {
    $name = Split-Path $p.path -Leaf
    if (Recorded $p.path) { continue }
    $copy = Join-Path $Out $name
    Copy-Item $p.path $copy -Force
    if ($p.map -and (Test-Path $p.map)) {
        # Cached maps are named by hash; ghostreel finds them by that name.
        Copy-Item $p.map (Join-Path $repo "testdata\maps") -Force
    }
    Say "Recording $name ($($p.why))"
    $log = Join-Path $Out ([IO.Path]::GetFileNameWithoutExtension($name) + ".log")
    $t0 = Get-Date
    # Start-Process can't redirect stdout and stderr to one file, so cmd does.
    $proc = Start-Process -FilePath "cmd.exe" -PassThru -NoNewWindow `
        -ArgumentList "/c `"`"$engine`" -sc2 `"$SC2`" -o `"$Out`" `"$copy`" > `"$log`" 2>&1`""
    $proc.WaitForExit()
    $secs = [int]((Get-Date) - $t0).TotalSeconds
    $ok = Recorded $p.path
    Say ("  {0} in {1} s (exit {2}); log: {3}" -f $(if ($ok) { "recorded" } else { "FAILED" }), $secs, $proc.ExitCode, (Split-Path $log -Leaf))
    $entry = [ordered]@{ replay = $name; why = $p.why; ok = $ok; seconds = $secs; exit = $proc.ExitCode; at = (Get-Date).ToString("s") }
    Add-Content -Path $manifest -Value ($entry | ConvertTo-Json -Compress) -Encoding UTF8
}
Say "Done. Recordings, replays and logs are in $Out; maps in testdata\maps."

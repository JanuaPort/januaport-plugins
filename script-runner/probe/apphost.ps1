param([string]$Image = 'k4-apphost:1048')
$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$probeDir = Join-Path ([IO.Path]::GetTempPath()) ('k4-apphost-' + [guid]::NewGuid().ToString('N'))
$containers = @()
$networks = @()
function Check-RAM {
    if ((Get-CimInstance Win32_OperatingSystem).FreePhysicalMemory -le 15GB/1KB) {
        throw 'Vor dem Containerstart sind mehr als 15 GiB freier RAM erforderlich.'
    }
}
function Docker-Checked {
    & docker @args
    if ($LASTEXITCODE -ne 0) { throw "Docker-Aufruf gescheitert: $($args[0])" }
}
try {
    New-Item -ItemType Directory -Path $probeDir | Out-Null
    @'
{
    admin 0.0.0.0:2019
    auto_https disable_redirects
}
http://:8085 {
    respond /healthz healthy 200
    respond 404
}
'@ | Set-Content -LiteralPath (Join-Path $probeDir 'Caddyfile')
    foreach ($name in @('k4-apphost-apps', 'k4-apphost-plugins')) {
        Docker-Checked network create --internal $name
        $networks += $name
    }
    Check-RAM
    Docker-Checked run -d --name k4-apphost-caddy --network k4-apphost-apps --network-alias k4-caddy -v "${probeDir}:/probe" caddy:2.11.7 caddy run --config /probe/Caddyfile --adapter caddyfile
    $containers += 'k4-apphost-caddy'
    Check-RAM
    Docker-Checked run -d --name k4-apphost-runner --network k4-apphost-plugins --network-alias k4-runner-control --user 65532:65532 --read-only --cap-drop ALL --security-opt no-new-privileges --entrypoint python3 $Image -m http.server 8090
    $containers += 'k4-apphost-runner'
    Docker-Checked exec k4-apphost-runner python3 -c "import urllib.request; print(urllib.request.urlopen('http://127.0.0.1:8090/').status)"
    $runnerIP = (& docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' k4-apphost-runner).Trim()
    if ($LASTEXITCODE -ne 0 -or !$runnerIP) { throw 'Runner-IP fehlt' }
    Check-RAM
    # Full package suite, including the actual Caddy and network tests; never -run.
    $containers += 'k4-apphost-suite'
    & docker run --rm --name k4-apphost-suite --network k4-apphost-apps --network-alias script-runner-apphost -v "${repo}:/repo" -v "${probeDir}:/probe" -v "$env:USERPROFILE/go/pkg/mod:/go/pkg/mod" -w /repo/script-runner -e K4_CADDY_ADMIN=http://k4-caddy:2019 -e "K4_RUNNER_IP=$runnerIP" golang:1.26.10 go test -count=1 -v ./internal/apphost
    $suiteExit = $LASTEXITCODE
    Docker-Checked logs k4-apphost-caddy
    if ($suiteExit -ne 0) { throw 'Volle Apphost-Suite mit Caddy rot; Paketzeilen oben lesen.' }
}
finally {
    # Only exact names created by this invocation. Never stop by image or pattern.
    foreach ($name in $containers) {
        & docker container inspect $name 2>$null | Out-Null
        if ($LASTEXITCODE -eq 0) { & docker rm -f $name | Out-Null }
    }
    foreach ($name in $networks) { & docker network rm $name | Out-Null }
    # Leave only non-secret test logs in the unique temp directory for review.
    Write-Host "Caddy-Probenverzeichnis: $probeDir"
}

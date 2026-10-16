param([string]$Image = 'jnpt1099-b1-agent-runner:test')
$ErrorActionPreference = 'Stop'
$prefix = 'jnpt1099-b1-smoke'
# Hilfs-Images per Digest gepinnt (Hinweis GATE, Release-Lauf 38982788721).
$busybox = 'busybox:1.37.0@sha256:bdf57e528e45e4433820e045b29b4597825a1c9e38353532d90a01445013f82e'
$curl = 'curlimages/curl:8.17.0@sha256:935d9100e9ba842cdb060de42472c7ca90cfe9a7c96e4dacb55e79e560b3ff40'
$inside = "$prefix-internal"
$outside = "$prefix-outside"
$runnerName = "$prefix-runner"
$canaryName = "$prefix-canary"
$createdContainers = @()
$createdNetworks = @()
# Zustand im Temp-Verzeichnis statt im Repo: auf Linux-Runnern blieb der Ordner nach dem Bind-Mount nicht löschbar.
$state = Join-Path ([IO.Path]::GetTempPath()) ("$prefix-" + [guid]::NewGuid().ToString('N'))
if (Test-Path -LiteralPath $state) { throw 'Probe state directory already exists' }
New-Item -ItemType Directory -Path $state | Out-Null
$keyFile = Join-Path $state 'runtime-key'
[IO.File]::WriteAllText($keyFile, "probe-runtime-key-not-a-secret`n")
function Check-Exit([string]$operation) {
    if ($LASTEXITCODE -ne 0) { throw "$operation failed: $LASTEXITCODE" }
}
try {
    docker network create --internal $inside | Out-Null
    Check-Exit 'create internal network'
    $createdNetworks += $inside
    docker network create $outside | Out-Null
    Check-Exit 'create outside network'
    $createdNetworks += $outside
    docker run -d --name $canaryName --network $outside $busybox httpd -f -p 8080 | Out-Null
    Check-Exit 'start local canary'
    $createdContainers += $canaryName
    docker run -d --name $runnerName --network $inside --read-only --cap-drop ALL --security-opt no-new-privileges:true --user 65532:65532 --mount "type=bind,source=$state,target=/run/jnpt/plugins/agent-runner,readonly" $Image | Out-Null
    Check-Exit 'start runner'
    $createdContainers += $runnerName
    docker exec $runnerName /agent-runner healthcheck
    Check-Exit 'healthcheck in distroless'
    $inspection = docker inspect $runnerName | ConvertFrom-Json
    if ($inspection[0].Config.User -ne '65532:65532' -or !$inspection[0].HostConfig.ReadonlyRootfs) { throw 'hardening differs' }
    if (@($inspection[0].NetworkSettings.Networks.PSObject.Properties).Count -ne 1) { throw 'additional network' }
    if (@($inspection[0].HostConfig.PortBindings.PSObject.Properties).Count -ne 0) { throw 'published port' }
    $network = docker network inspect $inside | ConvertFrom-Json
    if (!$network[0].Internal) { throw 'network not internal' }
    $canary = docker inspect $canaryName | ConvertFrom-Json
    $canaryIP = $canary[0].NetworkSettings.Networks.$outside.IPAddress
    $url = "http://${canaryIP}:8080/"
    docker run --rm --network $outside $curl -s -o /dev/null --connect-timeout 2 --max-time 3 $url
    Check-Exit 'positive network control'
    docker run --rm --network "container:$runnerName" $curl -s -o /dev/null --connect-timeout 2 --max-time 3 $url
    if ($LASTEXITCODE -eq 0) { throw 'runner reached outside-network canary' }
    Write-Output 'PASS: built image, UID 65532, read-only, healthcheck, one internal network, no published ports; outside-network canary blocked, positive control reachable.'
    docker image inspect $Image --format 'Image-ID={{.Id}} User={{.Config.User}}'
    Check-Exit 'image inspect'
}
finally {
    foreach ($name in $createdContainers) { docker rm -f $name | Out-Null }
    foreach ($name in $createdNetworks) { docker network rm $name | Out-Null }
    # Aufräumen darf das Ergebnis der Prüfungen nicht kippen: erst normal, dann per Container, sonst nur Warnung.
    try { Remove-Item -LiteralPath $state -Recurse -Force -ErrorAction Stop }
    catch {
        docker run --rm --mount "type=bind,source=$state,target=/s" $busybox sh -c 'rm -rf /s/* /s/.[!.]*' | Out-Null
        try { Remove-Item -LiteralPath $state -Recurse -Force -ErrorAction Stop }
        catch { Write-Warning "probe state not removed: $state" }
    }
}

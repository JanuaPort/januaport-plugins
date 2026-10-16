param([string]$Image = 'jnpt1099-b1-agent-runner:test')
$ErrorActionPreference = 'Stop'
$prefix = 'jnpt1099-b1-smoke'
$inside = "$prefix-internal"
$outside = "$prefix-outside"
$runnerName = "$prefix-runner"
$canaryName = "$prefix-canary"
$createdContainers = @()
$createdNetworks = @()
$state = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '.smoke'))
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
    docker run -d --name $canaryName --network $outside busybox:latest httpd -f -p 8080 | Out-Null
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
    docker run --rm --network $outside curlimages/curl:latest -s -o /dev/null --connect-timeout 2 --max-time 3 $url
    Check-Exit 'positive network control'
    docker run --rm --network "container:$runnerName" curlimages/curl:latest -s -o /dev/null --connect-timeout 2 --max-time 3 $url
    if ($LASTEXITCODE -eq 0) { throw 'runner reached outside-network canary' }
    Write-Output 'PASS: built image, UID 65532, read-only, healthcheck, one internal network, no published ports; outside-network canary blocked, positive control reachable.'
    docker image inspect $Image --format 'Image-ID={{.Id}} User={{.Config.User}}'
    Check-Exit 'image inspect'
}
finally {
    foreach ($name in $createdContainers) { docker rm -f $name | Out-Null }
    foreach ($name in $createdNetworks) { docker network rm $name | Out-Null }
    Remove-Item -LiteralPath $keyFile
    Remove-Item -LiteralPath $state
}

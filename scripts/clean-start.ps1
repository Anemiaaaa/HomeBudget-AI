$ErrorActionPreference = "Stop"

$projectName = "expenses-clean-check"
$port = "18080"
$baseUrl = "http://127.0.0.1:$port"
$env:HTTP_PORT = $port
$env:BOT_ENABLED = "false"

try {
    docker compose -p $projectName up -d --build db app

    $ready = $false
    for ($attempt = 0; $attempt -lt 40; $attempt++) {
        try {
            $response = Invoke-RestMethod -Uri "$baseUrl/readyz" -TimeoutSec 2
            if ($response.status -eq "ready") {
                $ready = $true
                break
            }
        } catch {
            Start-Sleep -Seconds 1
        }
    }
    if (-not $ready) {
        throw "clean-start app did not become ready"
    }

    $body = @{
        user_id = "990001"
        amount = "123.45"
        category = "other"
        spent_at = "2026-10-15"
        comment = "clean-start-check"
    } | ConvertTo-Json
    $created = Invoke-RestMethod -Uri "$baseUrl/api/v1/expenses" -Method Post -ContentType "application/json" -Body $body
    if ($created.amount -ne "123.45") {
        throw "clean-start create returned an unexpected amount"
    }

    docker compose -p $projectName restart db app
    $ready = $false
    for ($attempt = 0; $attempt -lt 40; $attempt++) {
        try {
            $response = Invoke-RestMethod -Uri "$baseUrl/readyz" -TimeoutSec 2
            if ($response.status -eq "ready") {
                $ready = $true
                break
            }
        } catch {
            Start-Sleep -Seconds 1
        }
    }
    if (-not $ready) {
        throw "restarted app did not become ready"
    }

    $list = Invoke-RestMethod -Uri "$baseUrl/api/v1/expenses?user_id=990001&query=clean-start-check" -TimeoutSec 5
    if ($list.total_count -ne 1 -or $list.items[0].amount -ne "123.45") {
        throw "expense did not survive the restart"
    }
    Write-Output "clean-start: passed"
} finally {
    docker compose -p $projectName down -v --remove-orphans
    Remove-Item Env:HTTP_PORT -ErrorAction SilentlyContinue
    Remove-Item Env:BOT_ENABLED -ErrorAction SilentlyContinue
}

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

    $secondBody = @{
        user_id = "990001"
        amount = "50.00"
        category = "transport"
        spent_at = "2026-10-14"
        comment = "clean-start-taxi"
    } | ConvertTo-Json
    $second = Invoke-RestMethod -Uri "$baseUrl/api/v1/expenses" -Method Post -ContentType "application/json" -Body $secondBody

    $thirdBody = @{
        user_id = "990001"
        amount = "25.00"
        category = "food"
        spent_at = "2026-10-13"
        comment = "clean-start-snack"
    } | ConvertTo-Json
    Invoke-RestMethod -Uri "$baseUrl/api/v1/expenses" -Method Post -ContentType "application/json" -Body $thirdBody | Out-Null

    $otherOwnerBody = @{
        user_id = "990002"
        amount = "999.00"
        category = "food"
        spent_at = "2026-10-12"
        comment = "clean-start-other-owner"
    } | ConvertTo-Json
    Invoke-RestMethod -Uri "$baseUrl/api/v1/expenses" -Method Post -ContentType "application/json" -Body $otherOwnerBody | Out-Null

    $firstPage = Invoke-RestMethod -Uri "$baseUrl/api/v1/expenses?user_id=990001&limit=2&offset=0" -TimeoutSec 5
    $secondPage = Invoke-RestMethod -Uri "$baseUrl/api/v1/expenses?user_id=990001&limit=2&offset=2" -TimeoutSec 5
    if ($firstPage.total_count -ne 3 -or $firstPage.items.Count -ne 2 -or $secondPage.items.Count -ne 1) {
        throw "clean-start pagination or owner filter returned an unexpected result"
    }

    $food = Invoke-RestMethod -Uri "$baseUrl/api/v1/expenses?user_id=990001&category=food" -TimeoutSec 5
    $foodSummary = Invoke-RestMethod -Uri "$baseUrl/api/v1/expenses/summary?user_id=990001&category=food" -TimeoutSec 5
    if ($food.total_count -ne 1 -or $foodSummary.count -ne 1 -or $foodSummary.total_amount -ne "25.00") {
        throw "clean-start filters or summary returned an unexpected result"
    }

    $updateBody = @{
        expected_version = $created.version
        amount = "150.00"
    } | ConvertTo-Json
    $updated = Invoke-RestMethod -Uri "$baseUrl/api/v1/expenses/$($created.id)" -Method Patch -ContentType "application/json" -Body $updateBody
    if ($updated.amount -ne "150.00") {
        throw "clean-start update returned an unexpected amount"
    }

    Invoke-RestMethod -Uri "$baseUrl/api/v1/expenses/$($second.id)?expected_version=$($second.version)" -Method Delete | Out-Null

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
    if ($list.total_count -ne 1 -or $list.items[0].amount -ne "150.00") {
        throw "expense did not survive the restart"
    }
    Write-Output "clean-start: passed"
} finally {
    docker compose -p $projectName down -v --remove-orphans
    Remove-Item Env:HTTP_PORT -ErrorAction SilentlyContinue
    Remove-Item Env:BOT_ENABLED -ErrorAction SilentlyContinue
}

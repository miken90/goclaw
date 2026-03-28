# GoClaw Local Coding Worker
# Polls VPS for coding tasks and executes them locally via Claude Code
#
# Usage:
#   $env:GOCLAW_WORKER_API_KEY = "gk_..."
#   pwsh -NoProfile -ExecutionPolicy Bypass -File .\local-coding-worker.ps1
#
# Config: $env:USERPROFILE\.goclaw-worker\config.json
# Logs:   $env:USERPROFILE\.goclaw-worker\logs\
# Runs:   $env:USERPROFILE\.goclaw-worker\runs\

#Requires -Version 7.0
Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# ── Color helpers ──────────────────────────────────────────────
function Write-Info    { param([string]$Msg) Write-Host "[✓] $Msg" -ForegroundColor Green }
function Write-Warn    { param([string]$Msg) Write-Host "[!] $Msg" -ForegroundColor Yellow }
function Write-Err     { param([string]$Msg) Write-Host "[✗] $Msg" -ForegroundColor Red }
function Write-Step    { param([string]$Msg) Write-Host "[→] $Msg" -ForegroundColor Cyan }
function Write-Debug2  { param([string]$Msg) Write-Host "    $Msg" -ForegroundColor DarkGray }

# ── Load config ────────────────────────────────────────────────
$configDir = Join-Path $env:USERPROFILE ".goclaw-worker"
$configPath = Join-Path $configDir "config.json"

if (-not (Test-Path $configPath)) {
    Write-Err "Config not found: $configPath"
    Write-Host @"

Create $configPath with:
{
  "vps_url": "https://goclaw.example.com",
  "team_id": "uuid-of-team",
  "worker_id": "windows-pc-01",
  "repo_key": "goclaw",
  "repo_path": "D:\\WORKSPACES\\PERSONAL\\goclaw",
  "worktree_base": "D:\\WORKSPACES\\PERSONAL\\goclaw-worktrees",
  "poll_interval_seconds": 10,
  "stale_worktree_ttl_hours": 24,
  "allowed_job_types": ["implement", "debug", "test", "review"],
  "heartbeat_interval_seconds": 30,
  "max_task_runtime_seconds": { "implement": 1800, "debug": 900, "test": 900, "review": 600 },
  "min_disk_free_gb": 1,
  "max_brief_bytes": 51200
}
"@
    exit 1
}

$config = Get-Content $configPath -Raw | ConvertFrom-Json

# ── API key ────────────────────────────────────────────────────
$ApiKey = $env:GOCLAW_WORKER_API_KEY
if (-not $ApiKey) {
    Write-Err "Set env var GOCLAW_WORKER_API_KEY before running"
    exit 1
}

# ── Ensure directories ────────────────────────────────────────
$logsDir = Join-Path $configDir "logs"
$runsDir = Join-Path $configDir "runs"
New-Item -ItemType Directory -Path $logsDir -Force | Out-Null
New-Item -ItemType Directory -Path $runsDir -Force | Out-Null
New-Item -ItemType Directory -Path $config.worktree_base -Force | Out-Null

# ── Log file ───────────────────────────────────────────────────
$logFile = Join-Path $logsDir "worker-$(Get-Date -Format 'yyyyMMdd-HHmmss').log"
function Write-Log {
    param([string]$Level, [string]$Msg)
    $ts = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
    "$ts [$Level] $Msg" | Out-File -FilePath $logFile -Append -Encoding UTF8
}

# ── Validate prerequisites ─────────────────────────────────────
Write-Step "Validating prerequisites..."

try { $null = & claude --version 2>&1 } catch {
    Write-Err "claude CLI not found in PATH. Install Claude Code first."
    exit 1
}
try { $null = & git --version 2>&1 } catch {
    Write-Err "git not found in PATH"
    exit 1
}
if (-not (Test-Path $config.repo_path)) {
    Write-Err "Repo path not found: $($config.repo_path)"
    exit 1
}

# Set long paths support
git config --global core.longpaths true 2>$null

Write-Info "Prerequisites OK"
Write-Info "VPS: $($config.vps_url)"
Write-Info "Team: $($config.team_id)"
Write-Info "Worker: $($config.worker_id)"
Write-Info "Repo: $($config.repo_path)"
Write-Info "Log: $logFile"

# ── HTTP helper ────────────────────────────────────────────────
function Invoke-WorkerAPI {
    param(
        [string]$Method = "GET",
        [string]$Path,
        [object]$Body = $null,
        [switch]$AllowError
    )
    $uri = "$($config.vps_url)/v1/teams/$($config.team_id)/worker$Path"
    $headers = @{
        "Authorization" = "Bearer $ApiKey"
        "Content-Type"  = "application/json"
    }
    $params = @{
        Uri     = $uri
        Method  = $Method
        Headers = $headers
    }
    if ($Body) {
        $params.Body = ($Body | ConvertTo-Json -Depth 10 -Compress)
    }
    try {
        return Invoke-RestMethod @params
    } catch {
        if ($AllowError) { return $null }
        $status = $_.Exception.Response.StatusCode.value__
        Write-Log "ERROR" "$Method $Path → HTTP $status"
        throw
    }
}

# ── Heartbeat background job ──────────────────────────────────
$script:heartbeatJob = $null
$script:currentTaskId = $null

function Start-Heartbeat {
    $script:heartbeatJob = Start-Job -ScriptBlock {
        param($VpsUrl, $TeamId, $WorkerId, $Key, $Interval)
        while ($true) {
            try {
                $headers = @{ "Authorization" = "Bearer $Key"; "Content-Type" = "application/json" }
                $body = @{ worker_id = $WorkerId; current_task_id = "" } | ConvertTo-Json -Compress
                Invoke-RestMethod -Uri "$VpsUrl/v1/teams/$TeamId/worker/heartbeat" `
                    -Method POST -Body $body -Headers $headers -ContentType "application/json" | Out-Null
            } catch { }
            Start-Sleep -Seconds $Interval
        }
    } -ArgumentList $config.vps_url, $config.team_id, $config.worker_id, $ApiKey, $config.heartbeat_interval_seconds
    Write-Debug2 "Heartbeat job started (every $($config.heartbeat_interval_seconds)s)"
}

function Stop-Heartbeat {
    if ($script:heartbeatJob) {
        Stop-Job -Job $script:heartbeatJob -ErrorAction SilentlyContinue
        Remove-Job -Job $script:heartbeatJob -Force -ErrorAction SilentlyContinue
        $script:heartbeatJob = $null
    }
}

function Send-Heartbeat {
    param([string]$TaskId = "")
    try {
        Invoke-WorkerAPI -Method POST -Path "/heartbeat" -Body @{
            worker_id       = $config.worker_id
            current_task_id = $TaskId
        } -AllowError | Out-Null
    } catch { }
}

# ── Stale worktree recovery ───────────────────────────────────
function Remove-StaleWorktrees {
    Write-Step "Checking for stale worktrees..."
    $base = $config.worktree_base
    if (-not (Test-Path $base)) { return }

    $ttlHours = $config.stale_worktree_ttl_hours
    if (-not $ttlHours) { $ttlHours = 24 }
    $cutoff = (Get-Date).AddHours(-$ttlHours)

    $dirs = Get-ChildItem -Path $base -Directory -ErrorAction SilentlyContinue
    foreach ($dir in $dirs) {
        if ($dir.LastWriteTime -lt $cutoff) {
            $taskNum = $dir.Name -replace '^task-', ''
            Write-Warn "Removing stale worktree: $($dir.Name)"
            try {
                git -C $config.repo_path worktree remove $dir.FullName --force 2>$null
                git -C $config.repo_path branch -D "task/$taskNum" 2>$null
            } catch { }
            if (Test-Path $dir.FullName) {
                Remove-Item -Recurse -Force $dir.FullName -ErrorAction SilentlyContinue
            }
            Write-Log "INFO" "Removed stale worktree: $($dir.Name)"
        }
    }
    # Also prune any git worktree metadata
    git -C $config.repo_path worktree prune 2>$null
}

# ── Trusted-task validation ───────────────────────────────────
function Test-TrustedTask {
    param([object]$Task)

    $meta = $Task.metadata
    if (-not $meta) {
        return "Task has no metadata"
    }

    # Job type check
    $jobType = $meta.job_type
    if (-not $jobType) { $jobType = "implement" }
    if ($jobType -notin $config.allowed_job_types) {
        return "Job type '$jobType' not in allowed list: $($config.allowed_job_types -join ', ')"
    }

    # Repo path check (case-insensitive on Windows)
    $repoPath = $meta.repo_path
    if ($repoPath -and ($repoPath.ToLower() -ne $config.repo_path.ToLower())) {
        return "Repo path '$repoPath' does not match configured: $($config.repo_path)"
    }

    # Brief check
    $brief = $meta.brief_markdown
    if (-not $brief) { $brief = $Task.description }
    if (-not $brief) {
        return "No brief_markdown or description found"
    }
    $briefBytes = [System.Text.Encoding]::UTF8.GetByteCount($brief)
    $maxBytes = $config.max_brief_bytes
    if (-not $maxBytes) { $maxBytes = 51200 }
    if ($briefBytes -gt $maxBytes) {
        return "Brief size ($briefBytes bytes) exceeds max ($maxBytes bytes)"
    }

    # Disk space check
    $repoDrive = (Split-Path $config.repo_path -Qualifier) -replace ':', ''
    $disk = Get-CimInstance Win32_LogicalDisk -Filter "DeviceID='${repoDrive}:'" -ErrorAction SilentlyContinue
    if ($disk) {
        $freeGB = [math]::Round($disk.FreeSpace / 1GB, 2)
        $minGB = $config.min_disk_free_gb
        if (-not $minGB) { $minGB = 1 }
        if ($freeGB -lt $minGB) {
            return "Disk space ($freeGB GB) below minimum ($minGB GB)"
        }
    }

    return $null  # validation passed
}

# ── Git worktree lifecycle ────────────────────────────────────
function New-TaskWorktree {
    param([int]$TaskNumber)
    $wtPath = Join-Path $config.worktree_base "task-$TaskNumber"
    $branch = "task/$TaskNumber"

    # Pre-create cleanup
    git -C $config.repo_path branch -D $branch 2>$null
    if (Test-Path $wtPath) {
        git -C $config.repo_path worktree remove $wtPath --force 2>$null
        Remove-Item -Recurse -Force $wtPath -ErrorAction SilentlyContinue
    }

    # Create worktree
    git -C $config.repo_path worktree add $wtPath -b $branch 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to create worktree at $wtPath"
    }
    return $wtPath
}

function Remove-TaskWorktree {
    param([int]$TaskNumber)
    $wtPath = Join-Path $config.worktree_base "task-$TaskNumber"
    $branch = "task/$TaskNumber"

    for ($i = 0; $i -lt 3; $i++) {
        try {
            if (Test-Path $wtPath) {
                git -C $config.repo_path worktree remove $wtPath --force 2>$null
            }
            git -C $config.repo_path branch -D $branch 2>$null
            if (Test-Path $wtPath) {
                Remove-Item -Recurse -Force $wtPath -ErrorAction SilentlyContinue
            }
            return
        } catch {
            Write-Warn "Worktree cleanup attempt $($i+1) failed, retrying..."
            Start-Sleep -Seconds ([math]::Pow(2, $i))
        }
    }
    Write-Err "Failed to cleanup worktree task-$TaskNumber after 3 attempts"
}

# ── Execute task ──────────────────────────────────────────────
function Invoke-CodingTask {
    param([object]$Task)

    $taskId = $Task.id
    $taskNumber = $Task.task_number
    $meta = $Task.metadata
    $jobType = if ($meta.job_type) { $meta.job_type } else { "implement" }
    $brief = if ($meta.brief_markdown) { $meta.brief_markdown } else { $Task.description }
    $agentId = if ($Task.owner_agent_id) { $Task.owner_agent_id } else { "" }

    $runDir = Join-Path $runsDir "task-$taskNumber"
    New-Item -ItemType Directory -Path $runDir -Force | Out-Null

    $startTime = Get-Date
    Write-Step "Executing task #$taskNumber ($jobType): $($Task.subject)"
    Write-Log "INFO" "Starting task #$taskNumber ($jobType): $($Task.subject)"

    # 1. Create worktree
    try {
        $wtPath = New-TaskWorktree -TaskNumber $taskNumber
        Write-Info "Worktree created: $wtPath"
    } catch {
        Write-Err "Worktree creation failed: $_"
        Invoke-WorkerAPI -Method POST -Path "/tasks/$taskId/fail" -Body @{
            reason   = "Worktree creation failed: $_"
            agent_id = $agentId
        } -AllowError | Out-Null
        return
    }

    # 2. Report progress
    Invoke-WorkerAPI -Method POST -Path "/tasks/$taskId/progress" -Body @{
        percent = 25; step = "worktree created, launching claude"
    } -AllowError | Out-Null

    # 3. Write brief to temp file (ADR-9: file-based prompt passing)
    $promptFile = Join-Path $env:TEMP "goclaw-task-$taskNumber.md"
    Set-Content -Path $promptFile -Value $brief -Encoding UTF8

    # 4. Launch Claude Code with timeout
    $outFile = Join-Path $runDir "stdout.txt"
    $errFile = Join-Path $runDir "stderr.txt"

    $maxSecondsMap = $config.max_task_runtime_seconds
    $maxSeconds = 1800
    if ($maxSecondsMap -and $maxSecondsMap.$jobType) {
        $maxSeconds = $maxSecondsMap.$jobType
    }

    $claudeArgs = @(
        "--print"
        "--dangerously-skip-permissions"
        "--output-format", "text"
        "--prompt-file", $promptFile
    )

    Write-Debug2 "Claude timeout: ${maxSeconds}s"
    Write-Debug2 "Working dir: $wtPath"

    $timedOut = $false
    $claudeExitCode = -1
    try {
        $process = Start-Process -FilePath "claude" -ArgumentList $claudeArgs `
            -WorkingDirectory $wtPath -PassThru -NoNewWindow `
            -RedirectStandardOutput $outFile -RedirectStandardError $errFile

        # Heartbeat during execution
        $elapsed = 0
        $halfProgress = $false
        while (-not $process.HasExited) {
            Start-Sleep -Seconds 10
            $elapsed += 10
            Send-Heartbeat -TaskId $taskId

            # Progress at ~50%
            if (-not $halfProgress -and $elapsed -gt ($maxSeconds / 2)) {
                Invoke-WorkerAPI -Method POST -Path "/tasks/$taskId/progress" -Body @{
                    percent = 50; step = "claude running ($elapsed`s elapsed)"
                } -AllowError | Out-Null
                $halfProgress = $true
            }

            # Timeout check
            if ($elapsed -ge $maxSeconds) {
                Write-Warn "Claude timed out after ${maxSeconds}s — killing process tree"
                $timedOut = $true
                Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
                Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
                    Where-Object { $_.ParentProcessId -eq $process.Id } |
                    ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }
                break
            }
        }
        if (-not $timedOut) {
            $claudeExitCode = $process.ExitCode
        }
    } catch {
        Write-Err "Claude launch failed: $_"
        $timedOut = $false
        $claudeExitCode = -1
    }

    # 5. Report progress
    Invoke-WorkerAPI -Method POST -Path "/tasks/$taskId/progress" -Body @{
        percent = 75; step = "claude completed, collecting results"
    } -AllowError | Out-Null

    # 6. Collect results
    $claudeOutput = ""
    if (Test-Path $outFile) {
        $claudeOutput = Get-Content $outFile -Raw -ErrorAction SilentlyContinue
        if ($claudeOutput.Length -gt 4000) {
            $claudeOutput = $claudeOutput.Substring(0, 4000) + "`n... (truncated)"
        }
    }

    $changedFiles = @()
    try {
        $changedFiles = @(git -C $wtPath diff --name-only HEAD 2>$null)
        if (-not $changedFiles) { $changedFiles = @() }
    } catch { }

    $duration = [int]((Get-Date) - $startTime).TotalSeconds

    # 7. Build and post result
    $resultPayload = @{
        task_id           = $taskId
        status            = if ($timedOut) { "fail" } elseif ($claudeExitCode -eq 0) { "pass" } else { "fail" }
        summary           = if ($claudeOutput) { $claudeOutput } else { "(no output captured)" }
        changed_files     = $changedFiles
        commands_executed  = @("claude --print --dangerously-skip-permissions --prompt-file ...")
        test_results      = ""
        branch            = "task/$taskNumber"
        blocker_reason    = if ($timedOut) { "Claude Code timed out after ${maxSeconds}s" } else { "" }
        duration_seconds  = $duration
    }

    # Save locally first (idempotency)
    $resultPath = Join-Path $runDir "result.json"
    $resultPayload | ConvertTo-Json -Depth 5 | Set-Content $resultPath -Encoding UTF8

    $resultJson = $resultPayload | ConvertTo-Json -Depth 5 -Compress

    if ($timedOut -or $claudeExitCode -ne 0) {
        $reason = if ($timedOut) { "Timeout after ${maxSeconds}s" } else { "Claude exit code: $claudeExitCode" }
        Write-Err "Task #$taskNumber FAILED: $reason"
        for ($i = 0; $i -lt 3; $i++) {
            try {
                Invoke-WorkerAPI -Method POST -Path "/tasks/$taskId/fail" -Body @{
                    reason   = "$reason`n`nOutput:`n$claudeOutput"
                    agent_id = $agentId
                }
                break
            } catch {
                $httpStatus = $_.Exception.Response.StatusCode.value__
                if ($httpStatus -eq 409) {
                    Write-Debug2 "Task already completed/failed — treating as success"
                    break
                }
                Start-Sleep -Seconds ([math]::Pow(2, $i))
            }
        }
    } else {
        Write-Info "Task #$taskNumber PASSED ($duration`s, $($changedFiles.Count) files changed)"
        for ($i = 0; $i -lt 3; $i++) {
            try {
                Invoke-WorkerAPI -Method POST -Path "/tasks/$taskId/complete" -Body @{
                    result   = $resultJson
                    agent_id = $agentId
                }
                break
            } catch {
                $httpStatus = $_.Exception.Response.StatusCode.value__
                if ($httpStatus -eq 409) {
                    Write-Debug2 "Task already completed — treating as success"
                    break
                }
                Start-Sleep -Seconds ([math]::Pow(2, $i))
            }
        }
    }

    Write-Log "INFO" "Task #$taskNumber completed: $($resultPayload.status) ($duration`s)"

    # 8. Cleanup
    Remove-Item -Path $promptFile -Force -ErrorAction SilentlyContinue
    Remove-TaskWorktree -TaskNumber $taskNumber

    Write-Info "Worktree cleaned up for task #$taskNumber"
}

# ── Graceful shutdown ─────────────────────────────────────────
$script:running = $true
$script:activeProcess = $null

$null = Register-EngineEvent PowerShell.Exiting -Action {
    $script:running = $false
    Write-Warn "Shutting down worker..."
    Stop-Heartbeat
}

trap {
    $script:running = $false
    Write-Warn "Interrupted — cleaning up..."
    Stop-Heartbeat
    if ($script:activeProcess -and -not $script:activeProcess.HasExited) {
        Stop-Process -Id $script:activeProcess.Id -Force -ErrorAction SilentlyContinue
    }
    break
}

# ── Main loop ─────────────────────────────────────────────────
Write-Host ""
Write-Host "╔══════════════════════════════════════════════╗" -ForegroundColor Cyan
Write-Host "║    GoClaw Local Coding Worker v1.0           ║" -ForegroundColor Cyan
Write-Host "║    Press Ctrl+C to stop                      ║" -ForegroundColor Cyan
Write-Host "╚══════════════════════════════════════════════╝" -ForegroundColor Cyan
Write-Host ""

# Startup tasks
Remove-StaleWorktrees
Start-Heartbeat

$pollInterval = $config.poll_interval_seconds
if (-not $pollInterval) { $pollInterval = 10 }

Write-Info "Entering poll loop (interval: ${pollInterval}s)"
Write-Host ""

while ($script:running) {
    try {
        # Poll for pending tasks
        $response = Invoke-WorkerAPI -Method GET -Path "/tasks?status=pending" -AllowError
        if (-not $response -or -not $response.tasks -or $response.count -eq 0) {
            Start-Sleep -Seconds $pollInterval
            continue
        }

        # Pick first pending task
        $task = $response.tasks[0]
        Write-Step "Found task #$($task.task_number): $($task.subject)"

        # Validate trusted-task policy
        $validationError = Test-TrustedTask -Task $task
        if ($validationError) {
            Write-Err "Task #$($task.task_number) rejected: $validationError"
            Write-Log "WARN" "Rejected task #$($task.task_number): $validationError"
            Invoke-WorkerAPI -Method POST -Path "/tasks/$($task.id)/fail" -Body @{
                reason   = "Worker validation failed: $validationError"
                agent_id = if ($task.owner_agent_id) { $task.owner_agent_id } else { "" }
            } -AllowError | Out-Null
            Start-Sleep -Seconds $pollInterval
            continue
        }

        # Claim the task
        $agentId = if ($task.owner_agent_id) { $task.owner_agent_id } else { "" }
        try {
            $claimed = Invoke-WorkerAPI -Method POST -Path "/tasks/$($task.id)/claim" -Body @{
                agent_id  = $agentId
                worker_id = $config.worker_id
            }
            Write-Info "Claimed task #$($task.task_number)"
        } catch {
            $status = $_.Exception.Response.StatusCode.value__
            if ($status -eq 409) {
                Write-Warn "Task #$($task.task_number) already claimed by another worker"
                Start-Sleep -Seconds $pollInterval
                continue
            }
            throw
        }

        # Execute the task
        $taskData = if ($claimed.task) { $claimed.task } else { $task }
        Invoke-CodingTask -Task $taskData

        Write-Host ""
    } catch {
        Write-Err "Poll loop error: $_"
        Write-Log "ERROR" "Poll loop: $_"
        Start-Sleep -Seconds $pollInterval
    }
}

# Cleanup
Stop-Heartbeat
Write-Info "Worker stopped."

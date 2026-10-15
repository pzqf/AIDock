[CmdletBinding()]
param(
    [ValidatePattern('^[a-z0-9][a-z0-9-]{0,48}$')]
    [string]$InstanceName = 'default',
    [switch]$NoOpen
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$demoRoot = Join-Path $repoRoot ".artifacts/experience-$InstanceName"
$projectRoot = Join-Path $demoRoot 'project'
$managedRoot = Join-Path $projectRoot '.aidock'
$dataRoot = Join-Path $demoRoot 'home'
$markerPath = Join-Path $demoRoot 'experience.json'
$utf8 = New-Object System.Text.UTF8Encoding($false)
[Console]::OutputEncoding = $utf8
$OutputEncoding = $utf8

# All instance paths are derived from a validated leaf name inside this repo.
# Existing data without our marker is never adopted or overwritten.
$initialized = Test-Path -LiteralPath $markerPath
if (-not $initialized -and (Test-Path -LiteralPath $demoRoot)) {
    throw "Instance directory already exists without a completed experience marker. Use a new -InstanceName. No data was changed: $demoRoot"
}
if ($initialized) {
    $marker = [IO.File]::ReadAllText($markerPath) | ConvertFrom-Json
    if ($marker.schema_version -ne 1 -or -not $marker.project_id) { throw 'Invalid experience marker. No sample data will be written.' }
    $projectId = [string]$marker.project_id
    $copyId = if ($marker.copy_id) { [string]$marker.copy_id } else { $projectId }
} else {
    $projectId = [guid]::NewGuid().ToString()
    $copyId = [guid]::NewGuid().ToString()
}

$binaryRoot = Join-Path $repoRoot '.artifacts/bin'
New-Item -ItemType Directory -Path $binaryRoot -Force | Out-Null
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss-fff'
$demoExe = Join-Path $binaryRoot "aidock-experience-$stamp.exe"
Push-Location $repoRoot
try {
    Write-Host 'Building AIDock...'
    & go build -o $demoExe ./cmd/aidock
    if ($LASTEXITCODE -ne 0) { throw 'AIDock build failed.' }
    $bindingArgs = @('--managed-root', $managedRoot, '--home', $dataRoot, '--project', $projectId, '--scope', 'server', '--copy', $copyId, '--native-root', $projectRoot)

    function Invoke-DemoCLI([string[]]$CommandArgs) {
        $output = & $demoExe @CommandArgs @bindingArgs --json
        if ($LASTEXITCODE -ne 0) { throw "AIDock operation failed: $output" }
        $result = $output | ConvertFrom-Json
        if (-not $result.ok) { throw "AIDock rejected operation: $($result.summary)" }
        return $result
    }
    function Save-DemoChange($Request) {
        $requestPath = Join-Path $demoRoot 'seed-request.json'
        [IO.File]::WriteAllText($requestPath, ($Request | ConvertTo-Json -Depth 12), $utf8)
        $preview = Invoke-DemoCLI -CommandArgs @('skill', 'plan', $Request.action, '--file', $requestPath)
        $null = Invoke-DemoCLI -CommandArgs @('skill', 'apply', '--plan', $preview.items.plan.id, '--expected-digest', $preview.items.plan.digest)
        return $preview.items
    }

    if (-not $initialized) {
        New-Item -ItemType Directory -Path $projectRoot, $dataRoot -Force | Out-Null
        Write-Host 'Creating four real sample skills in the isolated project...'
        $samples = @(
            @{name='api-conventions'; display='接口开发约定'; description='设计或修改 HTTP API 时，检查参数校验、错误结构与兼容性。'; tags=@('服务端','接口'); enabled=$true; document="# 接口开发约定`n`n先阅读项目现有路由与错误处理方式，再修改接口。`n`n- 在入口校验参数，返回明确的字段错误。`n- 延续已有响应结构，保持旧调用方兼容。`n- 为成功、错误及边界输入补充有意义的验证。`n- 交付时列出接口变化和验证结果。"},
            @{name='code-review'; display='代码审查'; description='审查代码变更中的正确性、回归风险和缺失的测试证据。'; tags=@('质量','审查'); enabled=$true; document="# 代码审查`n`n先理解需求与变更范围，然后沿实际调用路径检查。`n`n- 优先报告可复现的错误、数据风险和行为回归。`n- 指出相关文件、触发条件及影响。`n- 区分已验证结论和仍需验证的假设。`n- 没有发现问题时说明检查范围，不编造发现。"},
            @{name='commit-message'; display='提交说明'; description='提交前整理清晰的变更说明，让评审者理解修改目的和验证范围。'; tags=@('协作','Git'); enabled=$false; document="# 提交说明`n`n读取当前差异，只描述本次变更。`n`n标题以具体行为开头，说明修改的对象。正文按需要解释问题、解决方式和验证证据。不要包含无关文件，不把局部验证描述成完整验收。"},
            @{name='project-handoff'; display='项目交接'; description='任务暂停或转交时，记录完成状态、待办、关键路径和运行方式。'; tags=@('协作','交接'); enabled=$false; document="# 项目交接`n`n交接内容应包含：`n`n1. 当前目标与已经完成的工作。`n2. 尚未解决的问题及已知限制。`n3. 关键文件和可复现的运行、验证命令。`n4. 下一步可独立执行的任务。`n`n保留用户已有修改，明确说明哪些验证尚未执行。"}
        )
        foreach ($sample in $samples) {
            $created = Save-DemoChange @{action='create'; name=$sample.name; description=$sample.description; document=$sample.document}
            $null = Save-DemoChange @{action='metadata'; id=$created.skill_id; display_name=$sample.display; tags=$sample.tags}
            if ($sample.enabled) { $null = Save-DemoChange @{action='enable'; id=$created.skill_id} }
        }
        [IO.File]::WriteAllText($markerPath, (@{schema_version=1; project_id=$projectId; copy_id=$copyId} | ConvertTo-Json), $utf8)
    } else {
        # Only read/validate on subsequent starts; never reseed or re-enable skills.
        $null = Invoke-DemoCLI -CommandArgs @('skill', 'managed-list')
        Write-Host 'Reusing your existing experience data. Sample data was not reset.'
    }

    Write-Host "Experience project: $projectRoot"
    Write-Host 'Source management, checked native skill file installation, rollback/undo and isolated rehearsal are available. Session loading and replica publishing are not accepted yet.'
    Write-Host 'Keep this terminal running. Press Ctrl+C to stop, or use Close console in the page.'
    $consoleArgs = @('config') + $bindingArgs
    if ($NoOpen) { $consoleArgs += '--no-open' }
    & $demoExe @consoleArgs --json
    if ($LASTEXITCODE -ne 0) { throw 'AIDock console exited with an error.' }
} finally {
    Pop-Location
}

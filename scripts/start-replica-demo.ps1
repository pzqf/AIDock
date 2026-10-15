[CmdletBinding()]
param(
    [ValidatePattern('^[a-z0-9][a-z0-9-]{0,48}$')]
    [string]$InstanceName = 'default',
    [switch]$NoOpen
)
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$demoRoot = Join-Path $repoRoot ".artifacts/replica-experience-$InstanceName"
$markerPath = Join-Path $demoRoot 'experience.json'
$utf8 = New-Object System.Text.UTF8Encoding($false)
[Console]::OutputEncoding = $utf8
$OutputEncoding = $utf8
if ((Test-Path -LiteralPath $demoRoot) -and -not (Test-Path -LiteralPath $markerPath)) {
    throw "Unmarked instance exists; use a new -InstanceName. No data was changed: $demoRoot"
}
$binaryRoot = Join-Path $repoRoot '.artifacts/bin'
New-Item -ItemType Directory -Path $binaryRoot -Force | Out-Null
$demoExe = Join-Path $binaryRoot ("aidock-replicas-" + (Get-Date -Format 'yyyyMMdd-HHmmss-fff') + '.exe')
Push-Location $repoRoot
try {
    & go build -p 1 -o $demoExe ./cmd/aidock
    if ($LASTEXITCODE -ne 0) { throw 'AIDock build failed.' }
    $initialized = Test-Path -LiteralPath $markerPath
    if ($initialized) {
        $marker = [IO.File]::ReadAllText($markerPath) | ConvertFrom-Json
        if ($marker.schema_version -ne 1 -or $marker.copies.Count -ne 5) { throw 'Invalid replica experience marker.' }
    } else {
        New-Item -ItemType Directory -Path $demoRoot -Force | Out-Null
        $copies = @(1..5 | ForEach-Object { @{id=[guid]::NewGuid().ToString();name="测试副本 $_";root=(Join-Path $demoRoot "copy-$_/.aidock");local_excludes=@()} })
        $copies[4].local_excludes = @('skills/aidock-replica-guide')
        $marker = @{schema_version=1;project_id=[guid]::NewGuid().ToString();skill_id=[guid]::NewGuid().ToString();copies=$copies}
    }
    $dataRoot = Join-Path $demoRoot 'home'
    function Invoke-ReplicaCLI([string[]]$CommandArgs, [string]$SourceRoot) {
        $bindingArgs = @('--managed-root',$SourceRoot,'--home',$dataRoot,'--project',$marker.project_id,'--scope','server','--json')
        $copyBinding = @($marker.copies | Where-Object root -eq $SourceRoot)
        if ($copyBinding.Count -eq 1) { $bindingArgs += @('--copy',$copyBinding[0].id) }
        $output = & $demoExe @CommandArgs @bindingArgs
        if ($LASTEXITCODE -ne 0) { throw "AIDock operation failed: $output" }
        $result = $output | ConvertFrom-Json
        if (-not $result.ok) { throw "AIDock rejected operation: $($result.summary)" }
        return $result
    }
    function Save-SourceChange($Request, [string]$SourceRoot) {
        $requestPath = Join-Path $demoRoot 'request.json'
        [IO.File]::WriteAllText($requestPath,($Request | ConvertTo-Json -Depth 15),$utf8)
        $preview = Invoke-ReplicaCLI @('skill','plan',$Request.action,'--file',$requestPath) $SourceRoot
        $null = Invoke-ReplicaCLI @('skill','apply','--plan',$preview.items.plan.id,'--expected-digest',$preview.items.plan.digest) $SourceRoot
    }
    $currentRoot = [string]$marker.copies[0].root
    if (-not $initialized) {
        $packageRoot = Join-Path $demoRoot 'sample-package'
        New-Item -ItemType Directory -Path (Join-Path $packageRoot 'empty'),(Join-Path $packageRoot 'references') -Force | Out-Null
        $document = "---`nname: aidock-replica-guide`ndescription: 专用测试项目的技能配置比较样例。`n---`n# 副本配置约定`n`n这是独立验收数据，不引用真实工作目录。`n"
        [IO.File]::WriteAllText((Join-Path $packageRoot 'SKILL.md'),$document,$utf8)
        [IO.File]::WriteAllText((Join-Path $packageRoot 'references/notes.md'),"完整包附件也进入比较与基线。`n",$utf8)
        [IO.File]::WriteAllBytes((Join-Path $packageRoot 'asset.bin'),[byte[]]@(0,255,7))
        foreach ($copy in $marker.copies) {
            New-Item -ItemType Directory -Path (Split-Path -Parent $copy.root) -Force | Out-Null
            Save-SourceChange @{action='import';id=$marker.skill_id;package_path=$packageRoot;source_role='local-original'} $copy.root
            Save-SourceChange @{action='enable';id=$marker.skill_id} $copy.root
            $bindingPath = Join-Path $demoRoot 'binding.json'
            [IO.File]::WriteAllText($bindingPath,($copy | ConvertTo-Json -Depth 8),$utf8)
            $preview = Invoke-ReplicaCLI @('replica','register','--file',$bindingPath) $currentRoot
            $null = Invoke-ReplicaCLI @('replica','confirm','--plan',$preview.items.id,'--digest',$preview.items.digest) $currentRoot
        }
        $targetIds = @($marker.copies | Select-Object -Skip 1 | ForEach-Object { $_.id }) -join ','
        $preview = Invoke-ReplicaCLI @('replica','baseline-plan','--from',$marker.copies[0].id,'--to',$targetIds) $currentRoot
        $null = Invoke-ReplicaCLI @('replica','confirm','--plan',$preview.items.id,'--digest',$preview.items.digest) $currentRoot
        Save-SourceChange @{action='edit';id=$marker.skill_id;files=@(@{path='references/notes.md';content="源副本的附件修改。`n"})} $currentRoot
        Save-SourceChange @{action='edit';id=$marker.skill_id;files=@(@{path='references/notes.md';content="目标副本的独立附件修改。`n"})} $marker.copies[2].root
        Save-SourceChange @{action='disable';id=$marker.skill_id} $marker.copies[3].root
        # Dedicated sample outputs are initialized only for a new instance.
        # Reusing an instance never resets handwritten files or rehearsal data.
        foreach ($copy in ($marker.copies | Select-Object -Skip 1)) {
            foreach ($directory in @('.agents/skills','.claude/skills')) {
                $handwritten = Join-Path (Split-Path -Parent $copy.root) "$directory/aidock-personal-note"
                New-Item -ItemType Directory -Path (Join-Path $handwritten 'empty') -Force | Out-Null
                $personal = "---`nname: aidock-personal-note`ndescription: 独立体验项目中的手写技能，生成和回退均应保留。`n---`n# 手写说明`n`n这个包没有 AIDock 生成归属。`n"
                [IO.File]::WriteAllText((Join-Path $handwritten 'SKILL.md'),$personal,$utf8)
                [IO.File]::WriteAllBytes((Join-Path $handwritten 'attachment.bin'),[byte[]]@(2,0,254))
            }
            # Every copy keeps a distinct local rule. These overrides are not
            # shared source; generation must use the selected target's values.
            $profile = (Invoke-ReplicaCLI @('profile','show') $copy.root).items
            $edit = @{target='local';expected_sha256=$profile.layers.local_sha256;expected_layers_digest=$profile.layers.digest;profile=@{schema_version=2;name='local';rules=@("仅处理 $($copy.name) 的已确认范围；交付时说明回退入口。")}}
            $profilePath = Join-Path $demoRoot 'local-profile-request.json'
            [IO.File]::WriteAllText($profilePath,($edit | ConvertTo-Json -Depth 10),$utf8)
            $preview = Invoke-ReplicaCLI @('profile','plan','--file',$profilePath) $copy.root
            $null = Invoke-ReplicaCLI @('profile','apply','--target','local','--plan',$preview.items.plan.id,'--expected-digest',$preview.items.plan.digest) $copy.root
            foreach ($rulePath in @('CLAUDE.md','AGENTS.md')) {
                $manual = [byte[]](@(239,187,191) + $utf8.GetBytes("# $($copy.name) 手写规则`r`n这段内容应在生成、回退和撤销后保留。`r`n`r`n"))
                [IO.File]::WriteAllBytes((Join-Path (Split-Path -Parent $copy.root) $rulePath),$manual)
            }
        }
        [IO.File]::WriteAllText($markerPath,($marker | ConvertTo-Json -Depth 10),$utf8)
    } else {
        $null = Invoke-ReplicaCLI @('replica','list') $currentRoot
        Write-Host 'Reusing existing replica experience. No sample data was reset.'
    }
    Write-Host "Dedicated five-copy experience: $demoRoot"
    Write-Host 'Choose test copy 1 as publisher to inspect source change, two-writer conflicts and local exclusions. Project publishing is not enabled.'
    $consoleArgs = @('config','--managed-root',$currentRoot,'--home',$dataRoot,'--project',$marker.project_id,'--scope','server','--copy',$marker.copies[0].id,'--native-root',(Split-Path -Parent $currentRoot),'--json')
    if ($NoOpen) { $consoleArgs += '--no-open' }
    & $demoExe @consoleArgs
} finally { Pop-Location }

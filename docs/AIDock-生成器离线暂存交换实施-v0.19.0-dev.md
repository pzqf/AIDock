# AIDock 生成器离线暂存交换实施

日期：2026-10-15。版本：v0.19.0-dev。设计基线 v0.24 保持冻结。

阶段 1D 新增可以操作的外部生成器接入准备流程：检查合同与字节锁 → 确认复制独立暂存 → 检查离线返回包 → 查看历史 / 清理与中断重试。CLI、API 和 AI 工具管理页使用同一后端。

这是离线交换能力，不是外部执行适配完成。上一版回环 TCP / UDP 的隔离证据仍为 UNKNOWN；没有弱化网络门槛，没有修改防火墙，没有运行第三方生成器。1C / 1D / 1E 和阶段 1 总验收继续不勾选。

## 本次可用内容

| 操作 | 结果与边界 |
| --- | --- |
| 检查接入合同 | 读取显式适配文件、整个独立运行目录和当前已保存 owned 技能源；保存字节快照，不运行版本命令，也不从 PATH 寻找替代 |
| 准备暂存 | 必须确认预览摘要；按已锁定 blob 复制 runtime / input，保留二进制、脚本、空目录和权限；源、合同或依赖变化会阻断 |
| 检查返回包 | 只读取指定暂存的固定 output；回执必须绑定同一暂存、合同、输入、依赖和工具；实际输出与完整包清单逐项核对 |
| 查看记录 | 每个绑定有独立历史，核验当前源 / 合同 / 运行依赖；返回内容变化会使历史校验过期；重算摘要不能把缺失输出标为通过 |
| 清理与恢复 | 预览具体路径和文件数量，确认 observation_digest 后仅清理 owned 暂存；中断、锁定文件可重新查看并重试；不可识别内容保留 |

程序版本仅为声明值。锁定整个运行目录不证明所有系统库或动态依赖已经声明，也不证明程序格式、行为和隔离兼容性。离线回执只是输入绑定，不证明返回内容由指定程序生成。所有记录的 execution_ready / publication_ready 始终为 false。

## 第一个交换协议

协议：aidock-skill-mirror-v1。策略：offline-skill-mirror-exchange-v1。

适配 JSON 示例（替换为独立测试目录的真实绝对路径）：

```json
{
  "schema_version": 1,
  "id": "example-mirror",
  "declared_version": "1.0.0",
  "protocol": "aidock-skill-mirror-v1",
  "runtime_root": "D:\\AIDock-Test\\generator-runtime",
  "entry_relative": "generator.exe",
  "tools": ["claude-code", "codex-cli"],
  "network": "deny",
  "child_processes": "deny"
}
```

runtime_root 必须独立于当前技能源和 Local；入口为运行目录内的安全相对文件。全部运行依赖包括隐藏文件都进入快照；链接 / junction、设备路径、大小写冲突、超限内容、缺程序和未知工具阻断。沿用 workstore 的每文件 10 MiB、每树 256 MiB、20,000 项上限，不静默截断或忽略内容。

当前仅支持当前绑定的 enabled owned 包；不接管外部桥接源、继承包或 MCP 字段。源静态检查的 UNKNOWN / WARN 保持可见，离线复制不把它们提升为生效证据。支持的输出严格为 .claude/skills/&lt;name&gt; 和 .agents/skills/&lt;name&gt; 的完整镜像；不支持改写正文、只复制 SKILL.md、遗漏附件或增加 settings / MCP / 其他运行字段。

input/request.json 只包含协议、包 ID / 名称 / revision、输入及依赖摘要、工具选择和暂存绑定，不包含宿主根路径。完整包本身仍按原字节复制，不自动去除正文中的信息。output/result.json 回传请求的 schema_version、stage_id、stage_digest、adapter_sha256、runtime_digest、input_digest、tools；额外字段和多 JSON 对象拒绝。

返回包必须同时保留每个声明文件、权限和目录，包括空目录与二进制附件。尚未提供运行入口或“在外部运行”快捷命令；验收用返回内容由专用夹具合成。

## 持久记录与故障处理

Local 下按源根 / owner / scope 的摘要分开保存：

```text
external-generators/<binding>/
  plans/<UUID>.json             immutable plan
  states/<UUID>.json            PREPARING / STAGED / CLEANING / CLEANED
  validations/<stage>/<UUID>.json
generator-stages/<binding>/stage-<UUID>/
  .aidock-generator.json
  runtime/                     pinned complete dependencies
  input/                       skills + request.json
  output/                      declared returned packages + result.json
  scratch/
```

计划先落盘，再保存 PREPARING 状态，再创建独占目录和恢复标记，最后复制内容。成功后标为 STAGED；准备失败 / 进程突然退出保留可定位记录，并阻断此绑定新的暂存。计划写完而状态文件未写完、或空目录尚未来得及写标记的中断，也能通过独立记录确认并清理。

清理使用真实目录旁的 OS 锁，核对完整暂存观察和标记。转为 CLEANING 后再次检查字节，随后仅调整临时副本的只读属性并删除；原程序 / 原依赖的属性不变。标记最后删除，删除失败保留或重建标记。新内容、未知顶层项、链接、错误路径或已清理后重现的目录均阻断，不自动接管。

校验结果或备份 blob 损坏时不能复用 PASS；界面显示过期原因并允许清理合法 owned 暂存。计划或恢复身份损坏则保留内容，不扩大清理范围。历史与快照保留，清理可重复。未增加垃圾回收，历史储存占用仍需后续治理。

这沿用 workstore 的合作进程锁 / reparse 检查边界；不宣称可以防御管理员或同用户恶意进程在最终文件检查与删除之间持续替换路径。不宣称跨 OS 原子目录可见性。

## CLI 与 API

命令全部需要显式 --home 和当前 owned 源绑定：

```text
generator inspect --adapter <absolute JSON path>
generator stage --adapter <absolute JSON path> --digest <preview digest>
generator show --stage <UUID>
generator history
generator validate --stage <UUID> --digest <stage digest>
generator clean --stage <UUID> --digest <observation_digest>
```

inspect / stage / show / history / clean 成功退出 0；validate 完整性通过退出 0、报告阻断退出 5。输入错误 2、过期 3、绑定或不支持 4、恢复 / 清理冲突 6。上述退出 0 仅表示对应离线操作完成，不授权运行。generator run 固定退出 4 / GENERATOR_EXECUTION_UNAVAILABLE，不启动进程。

API：

```text
POST /api/generator/inspect   {adapter_path}
POST /api/generator/stage     {adapter_path,digest}
POST /api/generator/show      {stage_id}
GET  /api/generator/history
POST /api/generator/validate  {stage_id,digest}
POST /api/generator/clean     {stage_id,digest}
```

方法 / 一次性会话 / Host / Origin / CSRF / 严格请求字段 / 服务关闭 / 写操作串行门槛沿用现有控制台。校验报告 HTTP 信封可读取，STATIC_CHECK_FAILED 在报告内保持 FAIL，不能以 HTTP 200 当作通过。

## 验证与验收范围

- Windows 全仓 go test -p 1 -race -count=1 ./...、go vet -p 1 ./...、构建通过；Linux 程序及 generator 测试交叉编译通过，未在 Linux 执行。
- 新后端测试覆盖：完整包 / 二进制 / 空目录 / 脚本 / 权限 / 全依赖字节锁；缺失、额外、变动内容与错误回执；源及依赖过期；校验期间取消或变化不保存接受记录；不同绑定隔离；校验记录重算摘要仍不能采纳错误 PASS。
- 真实子进程突然退出后，新 manager 读取 PREPARING 并清理；Windows 锁定输出保留标记并重试；真实 junction 拒绝读取 / 删除且越界哨兵保留；只读临时依赖可清理，原属性保持。
- CLI / API 生命周期与退出码、认证、Origin / CSRF、方法、未知字段、关闭后拒绝操作通过；10 组界面状态测试和 Demo 58 项替代 DOM / timer 回归通过。
- 内置浏览器使用新独立 generator-experience-v019-20261015：预览 → 取消无暂存 → 确认暂存 → 空返回目录阻断 → 合成完整返回包通过；执行按钮始终关闭。草稿、清理确认 / 取消 / 内容变化阻断、历史与窄屏另有页面证据。
- 新专用项目的源 / 手写目标 / 原完整包 / 原运行目录 / 合同快照核对；既有五个测试副本及共同基线的 77 文件、98 目录保持原样。

没有扫描或修改 E:\\work、E:\\work2–5。没有运行第三方生成器、技能脚本、工具资格检查、原生安装或副本发布。

## 后续门槛

1. 补直接网络拒绝 / 强制出口治理的有效证据，并完成一个真实外部程序的隔离运行适配、程序身份与全部动态依赖约束。
2. 将经执行绑定的外部返回清单接入逐副本候选 / 检查 / 协调恢复；不能把本轮离线完整性记录当成该门槛已通过。
3. 在专用测试项目验收整批失败 / 中断 / 外部改动及实际会话加载，再评估真实发布；五个真实工作目录继续只读参考。

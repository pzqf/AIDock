# AIDock 原生事务与恢复预演实施 v0.10.0-dev

日期：2026-10-14。需求基线仍为 v0.24。本次属于阶段 1C 的事务与恢复增量，阶段 1 尚未完成。

## 用户可使用的流程

管理界面「AI 工具」先生成保存版本的完整包预览，再确认「预演本次应用」。系统建立独立项目，复制观察到的工具原始文件，再通过生产批次事务把完整技能包写入该独立项目的 `.claude/skills` 和 `.agents/skills`。原生 frontmatter、附件、二进制和空目录保留；不初始化 AI 工具或执行技能代码。

预演完成后可预览并确认回退、撤销回退、读取最近记录、恢复中断预演。每次预演有独立身份和恢复点，记录在页面刷新和程序重启后仍可读取。较旧预演的未完成事务优先显示，恢复前禁止开始新的预演。恢复失败后的历史事件保留，仍可回退最近成功的事务。

预演、绑定项目的工具应用、源保存和副本发布保持独立。预演结果不能提升为实际项目会话加载或能力生效证据。适配器 `verified_versions` 仍为空；顶层 `apply` 继续返回 `NATIVE_WRITE_UNAVAILABLE` / 4。不会通过预演接口接收调用方的根目录、可执行程序或 cwd。

## 事务与恢复

`internal/workstore.Batch` 保存 `native-targets` 类型的完整计划、每目标原始/候选树、内容寻址备份和持久 journal。写入前核对全部目标、完整摘要和全部备份；目标只能是当前绑定的固定目录，计划按 project/scope/copy 身份隔离。

锁位于实际项目边界旁，同一项目即使用不同 Local Root 也竞争同一 OS 锁。写入前先落 `prepared` 记录和项目恢复标记，再逐文件原子替换，最后持久保存 `committed`。逐文件可见性不等于整批原子可见性；进程中断由 journal 恢复。本次覆盖真实子进程退出，未做机器断电/文件系统故障验收。

第二目标失败或取消时整批自动恢复。恢复先核对所有目标的变更路径，每条路径必须仍是事务原始或候选字节；第二目标发生外部修改时，第一目标也不会先恢复。冲突返回 `RECOVERY_CONFLICT` / 6，保留目标和恢复点。恢复中再次中断可重试；目标暂存文件同时识别原始和候选字节前缀。

回退只逆转该事务触及的路径；其它手写文件保留。预览后任何目标变化都返回 `PLAN_STALE` / 3。回退本身仍先备份并走批次事务，撤销回退再逆转成功回退。自动创建的父目录仅在为空时清理；不删除外部文件。程序在提交记录保存后中断时，恢复只清理标记，不撤销已成功提交的内容。

现有路径从文件变目录或从目录变文件的升级尚未通过恢复验收，预览和批次校验在写入前明确阻断；不尝试执行类型互换后再让用户处理残留。此边界后续需另补结构转换及中断恢复用例。

未完成原生事务通过项目根的 `.aidock-native-pending.json` 阻断新源保存/编译；换 Local Root 或作用域不能绕过恢复身份。恢复资产损坏、未知路径、计划身份不符均拒绝恢复。备份、未完成记录和预演项目不自动清理。

## CLI 与 API

以下命令均携带同一显式绑定：

```powershell
# --managed-root <专用项目/.aidock> --native-root <专用项目>
# --home <隔离数据根> --project <UUID> --scope <scope> [--copy <id>]
aidock plan --all --json
aidock tool rehearsal run --plan <native-plan> --digest <digest> --json
aidock tool rehearsal show --json
aidock tool rehearsal plan --session <session> --transaction <latest-success> --json
aidock tool rehearsal apply --session <session> --plan <reverse-plan> --digest <digest> --json
aidock tool rehearsal recover --session <session> --json
aidock tool history --json
aidock tool recover --json
```

`tool rehearsal plan` 根据最近成功事务生成回退或撤销计划；`apply` 只接受同一预演的回退/撤销计划，拒绝源计划、seed、其它预演和路径逃逸。成功仅表示文件事务完成。

管理界面使用同一 Manager 方法：POST `/api/native/rehearsal/run`、`plan`、`apply`、`recover`，GET `/api/native/rehearsal/latest`。项目工具事务使用 GET `/api/native/transactions` 和 POST `/api/native/recover`。所有写请求继续使用进程会话认证、Host/Origin/CSRF 与严格 JSON；查询和恢复接口不接受目标路径。

## 本机验证

- 全仓 `go test -race ./...`（含可选真实 CLI 隔离发现）、`go vet ./...` 和构建通过；最终事务修改又运行 workstore/native/cli/console 的 race 回归。远端 CI 未运行。
- 五组 Node 状态回归与原 Demo 58 项回归通过，CI 已加入预演脚本语法和状态回归。
- 故障夹具覆盖第二目标失败、缺备份、错摘要、过期目标、prepared/暂存/第一目标完成/提交后的真实进程退出、恢复中再次退出、第二目标外部修改、跨 Local Root 锁竞争、源保存阻断、较旧未完成预演优先处理。
- 完整包夹具核对二进制、空目录、原生 frontmatter、手写内容以及源草稿；回退、撤销、重建 Manager 读取记录均通过。
- 内置浏览器使用专用体验项目，确认/取消、两工具应用预演、回退/撤销、重载读取和草稿保留通过；480px 页面没有横向溢出，console error/warn 为空。
- 体验项目 11 个源文件前后 SHA-256 一致；绑定项目的 `.claude` / `.agents` 均未创建。预演的四个 SKILL.md 来自两个已保存启用技能，未包含页面未保存的验收草稿。

最终体验项目为 `.artifacts/experience-recovery-release-20261014/`。截图 `.artifacts/aidock-recovery-v0.10.png`；磁盘核对 `.artifacts/recovery-evidence.json`；CLI 记录 `.artifacts/recovery-cli-result.json`。这些是本机隔离证据，不是实际工作项目或完整阶段 1 验收。`E:/work`、`E:/work2–5` 本轮未读取或修改。

## 下一步

继续补精确版本的实际项目发现/新会话正文加载，以及需要执行能力的 OS 隔离与授权证据。仅对通过全部门槛的能力开放绑定工具应用与回退，再推进 1D 的专用测试副本同步和 1E 联合验收。规则合并、JSON/TOML 所有权、受管 MCP、完整 Profile 多层合并和跨副本批次尚未因本次技能包预演而完成。

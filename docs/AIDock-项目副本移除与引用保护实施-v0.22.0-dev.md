# 项目与副本移除 · v0.22.0-dev

日期：2026-10-15。沿用冻结设计 v0.24 和阶段 1 计划；Goal 保持 active。

## 已开放流程

项目工作台和跨副本列表均可选择“移除登记”，先检查再预览，确认完整摘要后解除本机登记。项目目录、技能源、手写工具文件、历史备份、共同基线与删除 tombstone 保留；源根作为不变目标加入联合锁及恢复事务。取消预览不解除登记。

被移除成员的原身份和操作 ID 留在 canonical 登记中；旧 ID 不允许重新注册。用保留的配置重新接入时生成新 copy ID，保留 project/scope 身份，不沿用旧成员的共同基线。项目与副本 CLI、API、界面复用同一领域流程，没有第二份目录数据库。

CLI：

```powershell
aidock project list --home <数据根> --json
aidock project rm --id <入口ID> --home <数据根> --json
aidock replica rm --copy <副本UUID> --binding <当前入口ID> --home <数据根> --json
aidock project confirm --plan <计划UUID> --digest <完整摘要> --home <数据根> --json
aidock project history --home <数据根> --json
aidock project recover --plan <中断计划UUID> --home <数据根> --json
```

两种 `rm` 都仅生成预览；确认与恢复统一使用 `project` 命令。项目管理 API 增加 `POST /api/projects/remove`，只接受 `binding_id`。

## 引用与恢复边界

未完成事务先恢复。仍有可恢复真实源/工具目录的快照时返回 `BINDING_REFERENCED` 和类型、事务 ID、状态及原因，界面显示引用清单并禁用确认。原生快照按项目根检查全部历史身份，包含旧 CLI 的空/非 canonical copy 标识；损坏记录不能通过丢失 root 字段逃过检查。已完成的接入事务不支持反向恢复源，独立预演也不写真实项目，这些记录不锁住活动成员。

确认在取得源与登记全部根锁后重新核查引用，不只比较文件摘要。因此即使预览后新增快照、又把源还原为原字节，移除仍会被阻断。恢复路径不重新套用此门槛，避免中断后无法收口。

移除中断后可从项目操作历史核对两根范围并恢复登记。提交已持久保存后的恢复只收口本事务标记，不撤销成功移除。旧标签页用被移除入口读写时返回未登记错误；有未保存草稿时阻止当前项目移除。

**尚未完成**：有源/工具恢复引用的项目目前保留登记；引用保留、归档与解除活动成员的完整生命周期继续开发，不能把当前阻断当作最终管理体验。项目分组、库/桥接关联配置、完整 Profile/规则、L3/L4、隔离剩余门槛、正式副本发布、1E 及阶段 2–5 仍按计划推进。本版本不声明阶段 1 完成。

## 验收证据

- 项目、副本、事务底座、CLI、控制台五包 race：166 PASS、0 SKIP、0 FAIL。首次副本包达到人为设置的 180 秒总时限；保留失败日志，改为 600 秒后完整通过（196.6 秒）。原始记录为 `.artifacts/project-removal-v022/go-race*.jsonl`，合并接受记录为 `go-race-accepted.jsonl`；不是本轮全仓回归。
- 新增真实进程 `os.Exit(93)`，在登记写完后退出，新 Manager 恢复原登记和源。旧身份拒绝复用、重新接入、源字节不变、手写项保留、快照在预览后新增、旧 copy 原生快照和损坏日志的负向用例通过。
- 12 组前端状态脚本、全部脚本语法和冻结 Demo 58 项回归通过。`go vet ./...`、Windows 构建、Linux/amd64 交叉构建通过；Linux 未运行，没有远端 CI 或新的工具/模型集成验收。
- 内置浏览器：创建、预览取消、确认移除、逐字节文件保留、重新接入、实际技能保存后引用阻断、副本入口共用保护，以及外部测试进程中断后的界面恢复均通过。最终构建重启后状态保留，浏览器无控制台错误；680px 页面与对话框无横向溢出。
- 截图：`.artifacts/aidock-removal-v0.22.png`、`.artifacts/aidock-removal-v0.22-narrow.png`；数据均在 `.artifacts/project-removal-v022` 或 `t.TempDir()`。未访问或修改 `E:\work`、`E:\work2`–`E:\work5`。

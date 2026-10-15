# 项目归档与恢复登记 · v0.23.0-dev

日期：2026-10-15。沿用冻结设计 v0.24；阶段 1 尚未整体验收，持续 Goal 保持 active。

## 已开放流程

项目卡片支持“归档”，将原副本成员从活动导航和同步选择中隐藏，保留完整 Binding、原 copy ID、共同基线和归档时的恢复引用。项目文件、工具文件、备份和 tombstone 不删除。已归档项目可在项目首页及管理窗口的“已归档项目”中恢复登记；恢复后沿用原 ID，原回退记录继续可用。移除登记的引用保护继续保留，归档不是绕过该保护的物理删除。

归档、恢复均先预览，再确认完整摘要。取消不改变登记。有未保存草稿时阻止归档当前项目；旧标签页的源请求返回 PROJECT_ARCHIVED，保留草稿及项目管理恢复入口。旧链接恢复登记后显示“打开项目”，避免把未打开的项目误标为当前项目。

CLI 与界面使用同一项目领域流程：

```powershell
aidock project archive --id <入口ID> --home <独立数据根> --json
aidock project archived --home <独立数据根> --json
aidock project restore --id <入口ID> --home <独立数据根> --json
aidock project confirm --plan <计划UUID> --digest <完整摘要> --home <独立数据根> --json
aidock project history --home <独立数据根> --json
aidock project recover --plan <中断计划UUID> --home <独立数据根> --json
```

新增 API：GET /api/projects/archived，POST /api/projects/archive、/api/projects/restore（binding_id）。项目导航/历史/恢复不受旧 --binding 或请求绑定失效阻断；普通源操作仍验证活动身份。

## 事务与兼容

归档锁定源和 canonical registry，两根事务中源为不变目标；确认在全部根锁内复核快照引用。预览后新增快照，即使源字节随后恢复相同，也使计划过期。有未完成源/工具事务时先恢复，不允许归档。

恢复登记仅以 registry 为目标，不锁写源根、不修复源事务。因此独立路径客户端在归档期间留下源冲突时，仍能先恢复管理入口，再单独处理原源/工具恢复。恢复登记中断后通过项目操作历史恢复之前的归档状态，可重新预览恢复。操作历史按创建时间排列。

registry 新增可选 archived_copies；既有活动登记和 removed_copies 保持兼容。归档身份、重叠目录和同名重新接入被阻断，必须通过归档恢复原成员；已移除身份仍禁止复用。计划新增可选 references，旧计划摘要和历史读取兼容。

## 验收证据

- project、replica、cli、console 四包 race：117 PASS、0 SKIP、0 FAIL；完整日志位于 .artifacts/project-archive-v023/go-race.jsonl。本轮不是全仓 race。
- 新增真实 exit94：恢复登记的唯一 registry 目标写完后进程退出，新 Manager 恢复归档及引用后可重试。源快照实际 rollback、归档自动失败恢复、引用新增导致过期、源 conflict 不被恢复登记覆盖均通过。
- 12 组前端脚本、全部脚本语法、冻结 Demo 58 项、go vet ./...、Windows 构建与 Linux/amd64 交叉构建通过。Linux 未运行，未触发远端 CI，没有新增工具/模型会话验收。
- 内置浏览器独立项目：创建技能、草稿保留及归档阻断、预览取消/确认、引用展示、旧链接保护、恢复后原 ID 打开、原时间线及回退预览；真实中断后 UI 恢复范围仅 registry，通过。归档/恢复前后完整 .aidock 文件逐字节一致。
- 680px 页面/管理窗口无横向溢出；最终浏览器 error 日志为空。截图：.artifacts/aidock-archive-v0.23.png、.artifacts/aidock-archive-v0.23-narrow.png。验收数据在 .artifacts/project-archive-v023 和 t.TempDir()。未访问或修改 E:/work、E:/work2–5。

## 继续实施

项目库/桥接关联与分组、完整 Profile 继承/本机覆盖/规则、完整依赖/兼容、会话 L3/L4、隔离剩余门槛、正式副本发布、1E 和阶段 2–5 仍未完成。此版本只收口归档生命周期，不标记阶段 1 或 Goal 完成。

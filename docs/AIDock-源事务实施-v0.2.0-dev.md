# AIDock owned 技能源事务实施

日期：2026-10-13。版本：v0.2.0-dev，需求基线 v0.24。交付范围为阶段 1B 的 owned 项目技能源事务子集和对应最小界面，整个 1B 尚未验收。

## 工作目录边界

开发仅在 `D:/GitHub/MyAIDock`。`E:/work`、`E:/work2`–`E:/work5` 是真实工作目录，只可在授权范围内只读参考，禁止将它们用于创建、编辑、同步、恢复、生成或执行测试。本轮未再次扫描或修改这些目录。

所有 Go 写入测试使用 `t.TempDir()`，浏览器使用 `.artifacts/source-browser-<UUID>/` 下的合成临时夹具，包含两份合成 SKILL.md 和一份三字节二进制附件。未启动技能脚本、原生 AI 工具、插件或外部生成器。正式集成测试项目尚待另建。

## 已交付的能力

| 能力 | 实现与边界 |
| --- | --- |
| 显式源绑定 | `.aidock` 根、项目 UUID、scope、home 全部显式选择；HTTP 绑定由进程决定，客户端不能改根或项目；开发版仅支持 owned 项目源 |
| 创建/本地导入 | 创建完整原生 SKILL 包；导入必须明确 `local-original`，完整附件/二进制/脚本/空目录保留原字节；拒绝被盘点排除的文件、链接和含敏感/暂存文件的导入，不执行内容 |
| 编辑/版本 | 文本补丁、附件删除；二进制拒绝文本覆盖。稳定 UUID，完整树摘要 revision，不可变修订包含字节、文件模式和空目录；已有同摘要修订损坏时拒绝静默修复 |
| 本层启停 | Profile v2 UUID 开关与固定 revision pins；不解析完整继承合并，不应用到工具 |
| Profile 迁移 | v1 名称必须唯一映射 UUID/revision，未知/歧义/缺身份拒绝；保留其它 Profile 字段。保存后可源回退至原 v1 字节，再撤销回退 |
| 计划/保存 | 保存前完整源树/草稿备份，检查根、计划摘要和过期源；锁随实际目标根，不同 Local Root 竞争相同 OS 锁 |
| 自动恢复 | 持久 prepared → 单文件原子替换 → committed；失败/取消自动恢复；进程中断后拒绝新写入，明确 `skill recover` |
| 恢复冲突 | 目标只允许符合本次前/后状态，外部目标编辑返回 `RECOVERY_CONFLICT`/6 并保留现场；无关外部编辑保留；损坏资产在开始写入前返回 `BACKUP_INVALID`/5 |
| 手工回退/撤销 | 回到指定已提交源事务之前，保留完整修订缓存；回退本身也是可撤销的事务。首次初始化前空根暂不作为项目恢复点，界面明确提示 |
| 管理界面 | 创建、导入、文件编辑、本层启停、Profile 升级、路径预览、历史回退/撤销和恢复面板；表单修改/切文件使旧计划失效，迟到文件响应不覆盖当前选择；本页文本草稿随文件切换保留，全部已编辑文件纳入预览 |

浏览器未保存草稿仅存在页面内存，刷新页面会丢弃，界面明确提示。事务所备份的草稿指磁盘源树已有内容；不能把它描述为浏览器草稿持久化。

项目的 project.yaml 保留原字节；技能 optional metadata 及 Profile 其它字段保留。源保存、工具应用、副本传播保持独立，所有源操作结果标明 `pending_native_apply`。只读盘点入口不因此获得写入能力。

## CLI 契约

下面的路径必须是专用测试项目或隔离夹具，不得代入五个真实工作目录。所有源命令附相同绑定参数：

```text
--managed-root <专用测试项目/.aidock> --home <隔离数据根> --project <项目UUID> --scope <作用域> --json
```

源模式事务根固定为显式 home 的 `local`，不受个人 settings 的 Local Root 重定向；源根与事务根必须不重叠且无链接/reparse point。源根父目录须已存在，不自动接管无 project 身份的非空根。

```text
aidock skill plan create --file create.yaml <绑定参数>
aidock skill apply --plan <plan UUID> --expected-digest <预览摘要> <绑定参数>
aidock skill managed-list <绑定参数>
aidock skill history <绑定参数>
aidock skill recover <绑定参数>
aidock config <绑定参数>
```

`skill plan` 动作：create、import、edit、enable、disable、migrate、rollback。CLI apply 可以省略 expected-digest；界面确认保存必须带摘要，后台均检查计划自身摘要和完整当前源树。

```yaml
# create.yaml
name: project-guide
description: 专用测试技能
document: |
  使用本项目的约定。
```

import 描述包含 `package_path` 和 `source_role: local-original`，相对路径以描述文件目录为基准；edit 包含稳定 `id` 和 `files: [{path, content}]`，可用 `remove: true` 删除现有附件；enable/disable 包含 `id`；migrate 无需额外字段；rollback 包含 `snapshot: <已提交事务UUID>`，撤销回退引用回退事务 UUID。

数据目录：`<home>/local/source-transactions/<根摘要>/{blobs,plans,transactions}`。锁文件位于源根旁，正常结束保留锁文件但释放内核锁。源计划包含完整源树，不能当作脱敏后可公开共享的文件。

## 验证证据

- 本地 gofmt、`go vet ./...`、`go test -race ./...`：84 PASS、1 SKIP、0 FAIL，包含子测试。SKIP 为现有 Windows 普通 symlink 权限测试，未改变系统权限；新源/Local Root junction 拒绝测试通过。
- 持久事务测试覆盖完整字节/二进制/空目录/源草稿、第二文件写入失败、取消、预览过期、跨 Local Root 锁、跨根/种类/摘要/日志损坏、备份缺失。
- 独立测试子进程在暂存 Sync 后 rename 前、首文件替换后直接退出 37，验证内核锁释放、pending 阻断、fresh Store 恢复和重复 recover；外部目标改动阻止恢复，无关编辑保留。
- source 测试覆盖 create → edit → switch → rollback → undo、包导入不改原源、BOM、缺附件、名称/id 碰撞、项目隔离、镜像/插件拒绝、元数据保留、迁移原字节回退和修订损坏拦截。
- CLI/HTTP 与共享 engine 等价；未绑定不创建源/数据根，HTTP 拒绝无认证/Origin/CSRF、未知字段和多 JSON 对象，确认摘要必需，文件路径不能越界。
- Node：只读 UI 状态 5 PASS，源 UI 状态 8 PASS，旧 Demo 58 PASS；两个真实界面脚本语法检查通过，CI 已增加源脚本检查/回归。
- Windows 二进制与 Linux/amd64 交叉编译通过；尚未在 Linux 执行或触发远端 CI。
- 内置浏览器验收：合成源创建/编辑/启停、完整包导入、二进制禁改、预览失效、回退/撤销、初始化提示、切文件草稿及包含全部补丁、Profile 升级、认证刷新、默认宽度/768px/390px 布局、关闭后进程退出。最终无 console error/warn；截图位于 `.artifacts/aidock-source-{final,medium,narrow}.jpg`。此前只读界面的 17 项验收保持独立历史证据。

当前摘要与结构化边界见 [v0.2 实施清单](./AIDock-实施检查清单-v0.2.0-dev.json)。v0.1 清单和 v0.23/v0.24 归档不覆盖。

## 尚未通过的门槛

1. 外部原源授权桥接、全局技能库及固定继承/覆盖/分叉、原生改名、受引用删除/tombstone、批量启停、归档包导入、完整依赖/兼容解析尚未实现。
2. 目前是可恢复的逐文件批次，读取方可能看到中间文件状态；不承诺跨文件瞬时原子性。故障注入验证进程中断，未验证真实断电/OS 重启的磁盘行为。
3. 静态路径/link/reparse 检查不等于 OS 沙箱；同用户恶意目录替换竞态、Windows 排他目录句柄/ACL/broker、追加审计哈希链尚未完成。事务 JSON 是恢复记录，不能称完整安全审计。
4. Claude Code/Codex CLI 精确版本、生成/发现/新会话/能力探测、外部生成器、原生目标所有权、多副本三方同步和整批恢复仍待独立项目验收，当前未开放相关写入和执行。
5. 首次初始化前空根回退、浏览器草稿持久化/崩溃保留、历史 GC 和长时间大树/磁盘耗尽/真实同机竞态测试尚未交付。

下一步继续补齐 1B 的资产与引用管理，并在专用测试项目准备后推进 1A 安全原型与 1C 两工具应用；未完成门槛不标勾。没有提交、推送或发布。

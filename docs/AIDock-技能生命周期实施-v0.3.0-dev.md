# AIDock 技能生命周期实施

日期：2026-10-13。版本：v0.3.0-dev。需求基线仍为 v0.24。本次继续阶段 1B，交付当前显式绑定的 owned 项目源中的展示信息、原生改名、声明引用报告、可恢复退役及界面冲突修复；整个 1B 尚未验收。此前的 [v0.2 源事务记录](./AIDock-源事务实施-v0.2.0-dev.md) 保留为历史证据。

## 范围与实现

| 能力 | 已实现行为 |
| --- | --- |
| 展示名与标签 | CLI/HTTP/界面均支持修改及清空；只修改技能索引，稳定 ID、原生名称、包字节、修订和 Profile pin 不变；展示名限 80 字，标签按 Schema 检查 |
| 原生改名 | 独立 rename 计划，同一 owner/scope 检查名称冲突，包括退役包占用的名称；保持 UUID 和 `skills/<UUID>` 路径，生成新的完整包修订并更新当前 Profile pin，保留旧修订及其他技能固定的旧引用 |
| 字节保留 | 只替换 SKILL.md frontmatter 的 name 标量；保留 BOM、LF/CRLF、引号形式、注释、未知原生字段和正文；二进制、脚本、其他附件和空目录按完整源事务保留，不执行内容 |
| 引用报告 | 当前 owner/scope 下的 Profile 开关/pins、dependencies.skills、base_source、upstream；按 owner/scope/id 完整元组匹配；只说明声明引用，不声称完成依赖解析或运行生效检查 |
| 退役 | 必须提供原因。已声明的依赖、base、upstream 入站引用会阻止退役，包括可选声明和已停用来源中的声明。通过后保留完整包/修订，新增退役标记，并将本层开关设为停用 |
| 恢复管理 | 去除当前退役标记，保持本层停用；启用需要另行生成计划。退役期间拒绝启用、源编辑、改名、展示信息修改和重复退役 |
| 回退与撤销 | 退役标记、Profile 和源树进入同一事务；回退退役恢复此前状态，撤销回退重新恢复退役状态；变更记录位于绑定的 Local Root，并与计划摘要绑定 |
| 文件冲突修复 | 界面保存文本补丁时携带加载时 SHA256；从加载到预览之间的外部修改也会阻止覆盖。失败保留页面草稿；用户可两次点击确认后放弃当前文件草稿并重读磁盘，其他文件草稿保留 |
| 草稿与操作隔离 | 有未保存草稿时阻止该技能改名/退役。修改表单或切换操作使旧预览失效；仍保留 v0.2 的迟到响应拒绝、二进制禁改、切文件草稿和 pending 恢复阻断 |

改名对旧工具输出的清理标为 `PENDING_NATIVE_APPLY`，本次仅修改技能源。正文原生名调用与脚本动态引用仍为 UNKNOWN；本次不自动替换正文，也不重写其他技能的固定修订引用。

物理删除返回 `DELETE_SCOPE_UNKNOWN`/4。设计 §19.5 要求“未登记/离线引用范围未知时只允许停用/退役并保留修订，不自动物理清理”。其他项目、全局层与离线副本尚未登记，当前报告没有全局无引用保证，因此不开放清理包或历史版本。

## 存储与兼容

新增可选源文件 `skill-lifecycle.yaml`，Schema v1，包含当前 owner/scope 及每个退役技能的 ID、原生名、固定修订、时间、原因。旧 owned 源没有该文件时解释为空退役集合，不自动写入迁移。该标记是源状态；Local Root 的事务记录另外保存 `asset_effects`，用于描述同次源事务中的 active/retired 转换，不指示其他根写入。

```yaml
schema_version: 1
owner: 11111111-1111-4111-8111-111111111111
scope_id: server
retirements:
  - id: 22222222-2222-4222-8222-222222222222
    native_name: project-guide
    revision: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    retired_at: "2026-10-13T00:00:00Z"
    reason: 已被新版替代
```

拒绝未知字段、重复 ID、错误 owner/scope、无效时间、空白原因、索引名称/修订不匹配，以及退役技能仍启用/未显式停用的 Profile。源包发生未保存漂移时先阻止退役/改名，避免隐式吸收外部文件。源码编辑动作不能夹带原生改名。

`asset_effects` 为空时省略，v0.2 无此字段的计划摘要与应用仍兼容，已用旧序列化形状验证。Apply 重算源树的退役转换，拒绝伪造/不匹配记录；prepared、失败恢复和进程崩溃恢复继续使用同一完整事务协议。当前 JSON 事务记录不是追加审计哈希链。

兼容方向为旧源/计划由新版读取；程序降级不在本次支持范围。v0.2 二进制不理解退役标记，含退役状态的项目应继续使用 v0.3 或之后明确兼容的版本管理和恢复，不能把旧计划兼容当作旧程序兼容保证。

## CLI 与 HTTP

所有源操作仍要求显式 `.aidock` 根、隔离 home、项目 UUID 和 scope。下列路径只能代入专用测试项目，不得代入真实工作目录。

```text
aidock skill references <技能UUID> <绑定参数>
aidock skill plan metadata --file metadata.yaml <绑定参数>
aidock skill plan rename --file rename.yaml <绑定参数>
aidock skill plan retire --file retire.yaml <绑定参数>
aidock skill plan reactivate --file reactivate.yaml <绑定参数>
aidock skill apply --plan <计划UUID> --expected-digest <摘要> <绑定参数>
```

metadata 描述为 `id`、可选 `display_name` 和 `tags`，至少提供一项；空字符串/空数组用于清空。rename 描述为 `id`、`name`；retire 为 `id`、`reason`；reactivate 为 `id`。源动作拒绝夹带其他动作的字段，包括显式空值/null；未知字段仍严格拒绝。

新增认证 GET `/api/source/references?id=<UUID>`。其余动作沿用 `/api/source/plan` 和 `/api/source/apply`；页面进程绑定根，客户端不能换项目或路径。引用报告标明 `CURRENT_BOUND_SOURCE` 和 UNKNOWN 范围；`SKILL_REFERENCED` 错误携带报告供界面展示。

edit 的每项补丁新增可选 `expected_sha256`，与已读取文件摘要比较，不匹配返回 `PLAN_STALE`/3，源不变。管理界面始终携带；CLI 未携带时仍以生成计划时的完整树作为 apply 的过期检查基准。恢复冲突仍返回 6，保留现场。

## 验证证据

- 本地格式、vet、Go race：**136 PASS、1 SKIP、0 FAIL**，包含子测试。SKIP 为现有 Windows 普通 symlink 创建权限限制；junction 负向测试通过，未修改系统权限。
- 新测试覆盖名称/BOM/CRLF/引号/注释/未知 frontmatter/正文保留、稳定 UUID/路径、完整附件、旧修订和 Profile pin、名称占用、展示信息修改/清空、完整引用元组隔离、声明引用阻断、退役动作限制、恢复保持停用、回退与撤销、损坏退役元数据、伪造事务状态记录及加载到预览的外部修改。
- 故障注入在退役写入后失败，验证完整恢复；独立测试子进程在第二文件替换后退出 37，fresh Manager 恢复 marker/Profile/source，保留同次转换记录，重复 recover 无额外写入。旧计划兼容回归通过。此证据为进程崩溃恢复，不是断电或 OS 重启验收。
- CLI/HTTP 与共享源引擎测试通过，包括引用接口认证/方法限制、动作字段严格性、退役恢复和物理删除阻断。
- Node：只读状态 **5 PASS**，源管理状态 **18 PASS**，旧 Demo **58 PASS**；页面脚本语法通过。状态回归使用 DOM/HTTP 替身，和浏览器验收分别记录。
- Windows 二进制与 Linux/amd64 交叉编译通过；未在 Linux 运行，未触发远端 CI。
- 内置浏览器 **20 项检查通过**：展示信息保存、引用与未知范围、受引用退役阻断、改名/固定版本、退役/回退/撤销/恢复、未保存草稿保护、外部编辑拒绝覆盖与确认重读、认证刷新、1280/768/390px 布局和两次正常关闭。页面无 console error/warn；两个服务退出 0，临时 tab 已关闭，viewport 已复位。
- 浏览器操作后的磁盘核对另有 6 项：两包二进制/脚本保留、两包初始修订保留、改名仅改变 name 且 BOM/CRLF/未知字段/正文不变、恢复管理保持停用。

浏览器记录：`.artifacts/lifecycle-browser-evidence.json`；磁盘核对：`.artifacts/lifecycle-browser-disk-evidence.json`；截图：`.artifacts/aidock-lifecycle-{final,medium,narrow}.jpg`。本轮浏览器只操作 `.artifacts/lifecycle-browser-<UUID>/` 合成项目，Go 写入测试仅使用 `t.TempDir()`；没有扫描或修改 `E:/work` 与 `E:/work2–5`，没有执行技能脚本、AI 工具、生成器或插件，没有提交、推送或发布。

当前源码/文档摘要和门槛见 [v0.3 实施清单](./AIDock-实施检查清单-v0.3.0-dev.json)。v0.1/v0.2 证据及 v0.23/v0.24 归档保持历史状态，不覆盖。

## 下一步门槛

1. 1B 继续补齐全局技能库、固定继承/覆盖/分叉、外部原源桥接、完整依赖/兼容解析、批量操作和归档包导入。当前声明引用报告不能替代解析器、工具发现或能力探测。
2. 完整注册/离线引用目录、原生工具目标所有权与可验证清理尚未实现；物理删除、历史 GC、自动清理旧输出继续关闭。异常外部元数据要求人工核对；本次恢复能力针对已记录源事务，不自动修复任意损坏文件。
3. 浏览器未保存草稿仍只有本页内存，刷新/崩溃会丢失；当前按钮只支持明确确认放弃当前文件草稿并重读，没有持久草稿/三方合并。
4. 1A 的精确工具版本/发现入口和 Windows 隔离原型、1C 原生应用、1D 副本三方同步/整批恢复仍待专用测试项目。1E 联合验收、真实断电/OS 重启、目录替换竞态、追加审计哈希链均未通过。

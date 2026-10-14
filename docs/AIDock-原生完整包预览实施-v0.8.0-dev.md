# AIDock 原生完整包预览实施

日期：2026-10-14。版本：**v0.8.0-dev**。设计基线仍为 v0.24。本轮交付阶段 1A/1C 的只读编译与预览子集；没有完成原生写入、跨副本发布、OS 隔离或完整阶段 1。

## 已实现

- 两份独立版本化的适配器描述：`claude-code` → 项目 `.claude/skills`，`codex-cli` → 项目 `.agents/skills`。描述包含官方契约来源、核对日期、历史观察版本及精确版本验收白名单；目前白名单为空。描述随本版二进制内嵌，尚未实现外部安装/热更新适配器。
- Source Manager 单次导出已保存且启用的有效集合，使用现有 Profile 开关、固定 revision、全局继承、整包覆盖、分叉与外部批准缓存；不读取页面草稿或自动跟随上游候选。源包及声明先走同一静态预检。上游库或外部源离线时，仍使用项目缓存。
- 编译保持完整包原字节、frontmatter、二进制附件、空目录和平台内文件模式，输出目录使用原生名称。额外原生字段的跨工具语义保持 UNKNOWN；不将其转为规则文本。含 `.claude-plugin/plugin.json` 的包被技能预览阻断，不默许插件/钩子/MCP 激活。
- 工具目标必须由 `--native-root` 显式绑定，且为当前项目 `.aidock` 的父目录；不开放全局工具目标、不自动搜索项目或副本。项目源、只读全局库、Local Root 与工具目录重叠拒绝，外部登记及历史批准的权威根与目标重叠拒绝。目标的 link/junction、大小、路径与大小写冲突沿用完整树检查。
- 只读观察目标与拟议所有权清单 `.aidock-ownership.json`：未归属同名包即使字节相同仍报 `NATIVE_TARGET_COLLISION`；错误项目/作用域/工具/源根的清单拒绝接管；已有归属的包字节、目录或模式漂移报 `NATIVE_TARGET_DRIFT`。停用/改名后的旧输出也先检查漂移，其他包与手写文件保留。归属清单只是观察协议，不能独自证明授权或开启写入。
- 任一目标有静态 FAIL 时，批计划 `blocked=true`，该目标不给出可执行的路径变化；完整期望包和诊断仍可查看。UNKNOWN 不隐藏预览，但不能开放写入。
- 持久 `native-config` 计划存于 Local Root 的 `native-previews/<源根摘要>/plans/`，与 `skill-source` 计划分开；观察包/目标字节使用既有不可变 blob。计划绑定项目/作用域/副本、源快照、外部登记摘要、适配器描述、目标完整读入树与拟议输出。随机计划 ID/时间之外另有确定性 `content_digest`。
- `diff` 重新观察上述输入：源、目标、外部登记或适配器证据变化返回 `PLAN_STALE`；错误计划类型、确认摘要或损坏计划分别拒绝。进程重开后可重新核验已保存的预览。重新观察只证明观察时的输入一致，未来实际应用仍需在多目标锁下重新检查并完成事务恢复门槛。
- CLI、认证 HTTP 与控制台共用 Native Manager。页面新增「AI 工具与应用预览」，可选一个或两个工具、展开包内完整清单、查看拟议差异和阻断项、重新核验。切换工具或源编辑会清除旧预览；导航保留源草稿，并隐藏源保存确认区，避免混用计划。应用按钮保持禁用。
- 管理界面已统一采用 Demo 的 AIDock 外壳：蓝色主题变量、236px 分组侧栏、52px 顶栏、项目状态栏、内容区与右侧上下文面板；源管理、保存记录与回退、AI 工具预览继续复用原有 DOM ID 和事务边界。新增侧栏折叠、主题切换和右侧快捷入口，快捷入口只触发已有源/预览导航，不新增写入能力。
- 启动复查修正内容区与正文输入的重复 DOM ID，并增加唯一性回归断言；主题初始化移到同源外部脚本，遵守现有 CSP。内置浏览器验证真实正文载入、可编辑状态与无控制台错误。

## 官方契约与观察证据

使用 OpenAI Docs 工作流查找并实际获取了 [Codex 官方技能文档](https://developers.openai.com/codex/skills/)，同时获取 [Claude Code 官方技能文档](https://code.claude.com/docs/en/skills)。搜索 fallback 无直接官方结果；浏览器访问 Codex 页面返回 403，普通 HTTP 获取其官方带尾斜线页面成功返回 200，依据实际获取内容核对契约。网页副本放于本机 `.artifacts/`，不进入版本库。

Codex 文档要求目录内 `SKILL.md` 含 name/description，项目发现入口为从启动目录向仓库根的 `.agents/skills`；也存在用户/管理/系统技能、同名包、用户禁用配置及初始列表预算。Claude Code 文档支持项目 `.claude/skills`、完整附件目录，并列出企业/个人/项目优先级、commands 与插件入口等条件；`synced`、`anthropic-skills` 属于受限目录名称。本轮只编译显式项目层，未扫描用户目录或其他发现层，也未确认合并后的可见技能集合。

本机在仓库内仅执行 `codex --version` 与 `claude --version`，分别观察到 **Codex CLI 0.162.1** 与 **Claude Code 2.1.257**。未启动业务会话、模型请求、技能脚本或生成器。版本输出是实施时观察记录，不是运行时自动检查，也不是兼容验收；工具发现/会话加载没有因此被标记 PASS。

| 证据层 | 本轮状态 |
| --- | --- |
| L1 源与实际生成输出一致 | `PREVIEW_ONLY`；仅有完整拟议输出，没有实际原生应用 |
| L2 工具发现 | `UNKNOWN`；路径契约核对不能替代实际工具发现 |
| L3 新会话加载 | `UNKNOWN`；没有运行新会话，也不推断旧会话已更新 |
| L4 指定能力探测 | `UNKNOWN`；没有执行探针或技能脚本 |

## 使用入口

下例 `<测试项目>` 必须是专用隔离项目，不能使用 `E:/work`、`E:/work2–5`。其 `.aidock` 应先由源保存命令初始化；空目录不会被预览自动接管。`--home` 与源及工具目标目录不能重叠。

```powershell
aidock tool list --json

$binding = @('--managed-root', '<测试项目>/.aidock', '--home', '<隔离数据根>',
             '--project', '<项目 UUID>', '--scope', '<作用域>',
             '--native-root', '<测试项目>')

aidock plan @binding --all --json
# 单工具也可 --tool claude-code 或 --tool codex-cli
aidock diff @binding --plan <native-config UUID> --expected-digest <digest> --json
aidock config @binding
```

`plan` 成功返回 0 只表示预览生成；静态阻断返回 `NATIVE_PREFLIGHT_FAILED`/5，并仍包含完整报告。`diff` 需要预览 digest，输入变化返回 3。`apply --plan ... --expected-digest ...` 先重新核验类型/身份/摘要/新鲜度，存在 FAIL 返回 5，否则返回 `NATIVE_WRITE_UNAVAILABLE`/4；任何分支均不修改工具目标。`--digest` 是 `--expected-digest` 的同义参数。原有 `skill apply` 仍只处理源计划。

HTTP 使用进程已绑定的根，不接受客户端传入 root/owner/version：`POST /api/native/plan {tools:[...]}`、`POST /api/native/diff {plan_id,digest}`；`/api/native/apply` 保留同一阻断行为。沿用本地随机端口、会话、Origin/Host、CSRF、关闭与工作锁，不新增认证旁路。

## 验证及边界

Windows Go race、vet、构建及 Node 回归已验证。本轮测试覆盖完整包不变、有效集合离线固定版本、确定性与进程重开核验、目标碰撞/漂移/停用清理/幂等、错误归属/类型/摘要/损坏、外部权威根重叠、CLI/API 等价及目标始终只读。已有源事务/桥接/全局继承回归继续通过。

控制台状态回归包含原只读 5 项、源状态 50 项、外部桥接状态、新原生预览状态组；原 Demo 58 项仍通过。状态替身不当作浏览器验收。

内置浏览器使用独立 `.artifacts/native-preview-acceptance/` 项目完成两工具与单工具选择、附件/空目录展示、写入禁用、草稿往返保留、预览重新核验、目标变化后过期、同名手写目标阻断、刷新/会话恢复、源与工具确认区分离、窄屏和关闭验收；本轮另验收 Demo 外壳对齐、回退页/工具页切换、侧栏折叠、主题切换和右侧快捷入口，没有 console error。磁盘摘要确认源字节未变化，Codex 目标目录仍不存在，手写 Claude 目标原文保留。截图与本机检查记录放在同一目录，不提交。

Linux/amd64 可交叉编译本程序及 native/source 测试二进制；没有在 Linux 执行测试，不能据此宣称 Unix 文件模式或 Linux 运行验收。远端 CI 未触发。

本轮没有读取或修改 `E:/work`、`E:/work2–5`。本地提交，不推送、不部署。

后续优先完成隔离测试项目中的精确版本 L2/L3 发现与加载证据，随后实现可恢复的原生多目标应用、原生回退/撤销及故障/中断恢复；版本或隔离条件不足继续拒绝写入。源保存已有恢复机制仍可使用，但不能替代尚未实现的原生工具恢复。跨副本发布、外部生成器执行/写回和完整阶段 1 保持未完成。

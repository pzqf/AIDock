# 技能规则正式副本同步与整批恢复 · v0.61.0-dev

内置 owned 技能与规则候选通过逐目标、逐工具的发现及规则来源验证后，可以独立预览并确认同步到已登记项目。一次正式事务包含目标技能源、工具技能包、规则托管区块和共同基线，支持失败整批恢复、回退及撤销。

这是阶段 1D 的正式同步增量。完整 Profile、外部生成器的正式发布、实际技能 L3/L4 与阶段 1 总验收尚未完成，持续 Goal 保持 active。E:\work 与 work2–5 没有用于本轮扫描、测试或写入。

## 管理界面

“跨副本”页沿用逐项选边和候选检查流程，新增“预览正式同步”：

1. 选择目标和两款工具，显式选择“仅技能与规则”。
2. 逐项确认采用源、保留目标或跳过，准备候选；也可仅修复各目标自己的生成物。
3. 验证每个目标的候选工具发现和规则来源。历史记录可重新核对并恢复原候选。
4. 单独预览正式同步，核对发布者只读、实际目录、逐文件变化与确认摘要；取消不改项目。
5. 确认后从正式同步记录回退、撤销或恢复中断事务。记录绑定原发起目录及副本，切换到其它发起目录只显示该目录的记录。

“独立预演”和“正式同步”分别确认、分别记历史。检查记录本身仍不授权写入；完整 Profile 和外部返回包不能借用独立范围的资格。

启动初读遇到 SOURCE_BUSY 后，显式绑定的项目仍保留跨副本导航，不再必须刷新页面才能找回入口。界面统一显示“确认后同步”，移除旧的“正式发布继续开发”泛化说明。

## CLI 与认证 API

以下命令需带原有项目、作用域、副本和源目录绑定参数：

```powershell
aidock replica publication plan --file request.json
aidock replica publication apply --publication <id> --digest <sha256>
aidock replica publication history
aidock replica publication show --publication <id>
aidock replica publication reverse --publication <id> --transaction <transaction-id>
aidock replica publication apply-reverse --publication <id> --plan <plan-id> --digest <sha256>
aidock replica publication recover --publication <id>
```

`request.json` 使用 `generation_id`、`digest`、`check_id` 和 `check_digest`。认证 API 为 `/api/replica/publication/plan`、`apply`、`show`、`history`、`reverse`、`apply-reverse` 和 `recover`；只有 history 使用 GET，其余为 POST。确认请求使用 `publication_id`、`digest`、`transaction` 或 `plan_id`。

## 输入资格、锁与恢复

- 只接受内置 owned、版本 5、显式 `skills-rules`、无阻断且有变化的候选。检查策略为 `owned-candidate-inspection-v5`。
- 每个工具的精确版本、程序字节、输入摘要、候选目录、范围及完整诊断必须匹配；规则另需明确探测、精确路径与正文 SHA、`RULES_ISOLATED_LOAD` 证据。
- 正式计划内嵌完整源决策、生成候选和检查；提交和回退事务另有完整字节备份。删除原候选、生成或检查文件后，仍可回退、撤销和恢复。
- 提交前重建每一个允许修改的字节，拒绝重算摘要后扩展到工具 settings、未选源、额外基线或重复只读输入。
- 发布者及未改写源持只读源锁；所有选中副本的全局库和本机 Profile 共同加锁。首次本机覆盖尚不存在时也持锁，不创建覆盖正文。
- 目标源、固定工具容器、规则文件与共同基线共同持锁。工具容器中只允许技能子目录变化，其余字节保留；同时持普通原生应用的项目锁，阻止两种事务互相绕过。
- 全部写入并复核 After 树后，commit 前再检查发布者、只读副本、配置、分支、规则优先级和工具程序。外部变化或取消使整批恢复；发布者的外部编辑保留。
- 未完成的协调屏障阻断普通原生应用、回退和恢复。必须从原绑定的正式事务入口整批恢复；恢复不依赖过期候选或当前工具探测。

共享 Profile 仍按照明确源决策传播，其中可能包含已有 MCP 启停选择；本机授权、凭据和 MCP 原生连接不随行。独立范围不表示 MCP 条件已满足。

## 本轮验证

证据仅位于 `.artifacts/experience-publication-locks-v061/` 和 Go 临时项目。

| 验证 | 结果 |
| --- | --- |
| source/workstore/native/replica/cli/console 完整兼容回归 | 六包退出 0；真实集成另外显式运行，默认跳过不计通过 |
| 定向竞态 | 集合锁、最终输入 guard、取消、原生互斥、恢复屏障与资格负向测试退出 0 |
| 五副本真实工具集成 | 两款工具共 8 个候选发现与 8 个规则来源，正式提交、回退、撤销、纯生成物修复通过 |
| 故障注入 | 第二副本失败、发布者外部编辑、首次本机保存互斥及五个时点子进程退出恢复通过 |
| 计划篡改与原候选删除 | 非托管文件/源/基线/重复输入拒绝；原候选删除后仍可回退恢复 |
| 发起绑定隔离 | 另一副本的历史为空且不能打开原发起记录；原发起者历史仍有效 |
| 独立 CLI | 7 项流程通过，包含静态证据拒绝、错误摘要拒绝、21 根提交和回退/撤销 |
| 认证 API | 预览、错误摘要、提交、回退、撤销、history/show 和幂等恢复通过 |
| 内置浏览器 | 恢复候选、取消、正式同步、回退、撤销、再次回退及交付构建重启历史恢复；各步核对 21 根字节 |
| 前端 | 27 组状态测试与全部 JS 语法检查通过，覆盖忙碌、候选切换和迟到响应 |
| 构建与静态检查 | gofmt、go vet、Windows 构建通过；Linux 仅交叉构建，不代表平台运行验收 |

工具版本为 Claude Code `2.1.257` 与 Codex CLI `0.162.1`。发现和规则来源仅证明独立临时项目中的观察；没有执行技能、启动第三方生成器或发送模型请求，实际项目技能 L3/L4 仍为 UNKNOWN。

初次 CLI 脚本把带点工具目录的锁侧车少写一个点，导致最终全树断言失败；产品提交/回退流程均已完成。保留初始日志，修正为精确锁元数据名称后完整重跑通过。排除仅限原先不存在的声明锁文件，业务文件仍逐字节比较。

证据索引为 `verification.json`，主要日志包括 `compatibility-final-result.json`、`focused-race-result.json`、`publication-final-result.json`、`publication-binding-regression-result.json`、`cli-acceptance.json`、`api-acceptance.json` 及 `browser-*.json` / `browser-*.jpg`。

## 剩余工作

正式同步在人工编辑后恢复的冲突交互、正式路径的 junction 越界及任意副本发起的联合验收继续补齐；底层 Coordinator 的相关测试不替代全部上层场景。完整配置联合事务、外部生成器隔离与执行、实际会话/能力、阶段 1E 联合记录及后续阶段仍按计划推进。阶段 1 总验收不提前勾选。

# 副本技能规则独立生成与恢复 · v0.60.0-dev

启用 MCP 的副本可以显式选择“仅技能与规则”，检查和预演所选技能目录及规则文件，保留逐副本 MCP 诊断。默认完整 Profile 检查继续阻断未满足的 MCP 条件。源、生成物、规则及共同基线可在独立副本联合回退和撤销。

这是阶段 1D 的内置候选流程增量。正式副本发布、外部生成器执行、完整配置联合事务、实际 L3/L4 以及阶段 1 的其余验收仍未完成，持续 Goal 保持 active。

## 用户入口

跨副本页比较三方差异后，逐条选择采用源、保留目标或跳过，再选择工具及“工具生成范围”：

- **完整 Profile 检查（默认）**：沿用完整配置条件。
- **仅技能与规则**：只将源编译器标记为 `component: mcp` 的诊断保留在范围外。每个目标显示自己的原诊断；MCP 原生连接仍由独立流程管理。

范围选择同时用于“预览所选处理方案”和“仅预览生成物修复”。切换范围会清除当前候选、检查和确认弹窗，保留比较及逐项选择。预览、探测确认、逐副本结果、历史和预演详情均显示实际绑定的范围。重启后可由历史恢复原候选；页面的其它选择不能替换它的范围或摘要。

**工具生成范围不改写源决策。** 共享 Profile 组仍按逐条选择传播，包括所选组原有的 MCP 启停选择；目标本机覆盖和 MCP 授权独立保留，权限不会因同步而取得。范围不表示 MCP 已修复或可连接。

阻断时，未编译的规则明确显示“等待解决阻断后编译”。页面可提示显式选择独立范围；技能、规则和一般 Profile 错误继续阻断。

## CLI 与 API

```powershell
aidock replica generation stage --plan <resolution-id> --digest <resolution-digest> --tools claude-code,codex-cli --application-scope skills-rules
aidock replica generation check run --plan <generation-id> --digest <generation-digest> --probe --rule-probe
aidock replica rehearsal run-generated --plan <generation-id> --digest <generation-digest>
```

上述命令仍需原有的项目/作用域/副本及源绑定参数。省略 `--application-scope` 或指定 `full` 使用完整条件。

`replica generation repair --file <descriptor>` 的 JSON 新增可选 `application_scope`，其它字段为 `from`、`targets`、`comparison_digest` 和 `tools`；修复不采纳发布者源或推进真实共同基线。

`POST /api/replica/generation` 和 `/api/replica/generation/repair` 使用同一字段。检查和预演只接收已持久化清单的 UUID 与摘要，不重新接受范围。外部返回包保持原合同，不增加此字段。

## 持久输入、兼容与恢复

- 默认生成清单仍为版本 4；显式独立范围使用版本 5。旧版 1–4 不允许携带新范围，外部返回包仍为版本 3。
- 清单保存 `application_scope`，每个目标保存 `excluded_checks`。完整源候选、配置摘要、输出观察及源决策保持原样，只有编译器临时输入使用过滤视图。
- 检查前、显式探测后及预演前，逐副本重新编译完整输入，精确核对生成物和范围外诊断。仅重算 JSON 摘要不能改变范围、删除诊断或将非 MCP 错误排除。
- 范围和原诊断进入原生候选检查输入摘要与报告；汇总检查独立核对逐副本绑定。检查策略升级为 `owned-candidate-inspection-v4`，旧检查保留为历史并要求重新检查。
- MCP 定义、本机/全局配置、源、工具版本、规则文件或优先级输入变化仍可使候选或检查过期。未缩减完整配置或源的过期判断。
- 沿用现有固定根协调事务和恢复屏障。技能目录、规则单文件、源及基线共同恢复；MCP JSON/TOML 和连接归属侧车不作为本次写入目标。

现有技能依赖、执行需求、未知原生元数据、槽位碰撞、规则来源及一般 Profile 条件没有豁免。不会启动 MCP、外部生成器、技能脚本或模型请求，不签发原生安装资格。

## 本轮验证

仅使用 `.artifacts/experience-replica-application-scope-v060/` 与测试临时目录；未访问五个真实工作区。

| 验证 | 结果 |
| --- | --- |
| native/replica/cli/console 定向竞态 | 退出 0，60 条通过记录，无失败 |
| native/replica 完整兼容回归 | 退出 0，244 PASS / 4 个显式集成测试 SKIP / 0 FAIL |
| 前端状态与语法 | 26 组状态检查及 26 个 JS 文件通过 |
| 静态检查与构建 | gofmt、go vet、Windows 构建通过；Linux 仅交叉构建 |
| 全新五副本 CLI | 9 项流程通过，包括默认阻断、显式范围、非法输入、真实探测、历史恢复、联合预演、回退、撤销及纯修复 |
| 认证 API | 两范围、原候选恢复、非法范围和纯修复通过；登记项目全树保持 |
| 内置浏览器 | 四目标逐条选择、范围切换、取消探测、双工具规则来源、联合预演/回退/撤销及重启历史恢复通过 |
| 完整文件核对 | 21 根恢复范围；4 个目标源、8 个技能目录、8 个规则文件和基线核对通过；登记项目全树、手写正文及 MCP 原字节保持 |

真实探测版本为 Claude Code `2.1.257` 与 Codex CLI `0.162.1`，两工具共 8 份完整候选技能及 8 份规则来源通过。L2 只证明独立临时项目发现；实际项目新会话正文加载、能力执行及模型遵从仍为 UNKNOWN。四个默认跳过的显式集成测试没有算作通过，本轮独立 CLI 与浏览器另行运行真实发现及规则探测。

保留初次准备缺父目录的失败夹具和日志，修正准备步骤后在全新 `fixture-final` 中重跑通过。跨 REPL 对象的原型比较曾使验证脚本产生假失败，改为逐路径及字节摘要比较后，21 根回退和撤销核对通过。没有删除失败证据。

证据入口：`verification.json`、`focused-race.log`、`compatibility-tests.log`、`cli-acceptance.json`、`api-acceptance.json`、`browser-rollback.json`、`browser-undone.json`、`browser-restored-scope.png`。

## 下一步

继续收口正式副本发布及整批恢复，核对内置生成路径与外部生成器各自的能力门槛，再推进阶段 1 其余必做。外部执行与隔离、正式发布等验收不会因本轮候选通过而自动开放，阶段 2–5 不提前声明完成。

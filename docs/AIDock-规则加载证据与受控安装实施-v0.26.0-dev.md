# 规则加载证据与受控安装 · v0.26.0-dev

日期：2026-10-15。继续冻结设计 v0.24 的阶段 1，不修改历史设计归档。Goal 保持 active。

## 已开放

`tool check --tool codex-cli --probe --rule-probe` 和界面「验证规则加载」共用后端。规则验证必须显式与 probe 一起开启；默认静态检查及普通技能发现不创建规则会话。界面先确认可执行范围，取消零写入。

精确版本 Codex CLI 0.162.1：使用独立项目、独立空工具 home 和仓库边界，复制待编译的 AGENTS.md 完整候选；通过 app-server 的 initialize → initialized → thread/start 建立 ephemeral、read-only、approvalPolicy=never 的临时会话。禁用 MCP 配置与 web_search，不继承凭据或项目工具配置，不发送 turn/start/模型请求。工具须只返回唯一候选的 instructionSources，候选前后字节一致且不超过显式固定的 32 KiB 指令预算。进程沿用超时与 Windows Job Object 整树清理。

通过证据记录规则路径、候选 SHA256、程序字节与版本、源/目标/适配器摘要、有效期。当前检查通过后，`plan --tool codex-cli --check <UUID>` / `apply --plan <UUID> --digest <摘要>` 可联合安装技能目录及规则文件；手写段保留，使用已有多目标事务、完整快照、回退和撤销。检查变化或过期拒绝写入。

同目录非空 AGENTS.override.md 会遮盖 AGENTS.md，明确返回 RULES_SHADOWED；两个文件均保留。该优先级输入纳入计划与证据摘要，保存检查前及实际事务前再次核验，出现较晚的外部修改同样在写入前阻断。

Claude Code 2.1.257 的规则协议尚未取得同等无模型证据，Rules=UNKNOWN；技能发现不能替代。选两工具时整批规则安装仍关闭，可保留预览与独立预演。用户可以明确只选已具备证据的 Codex CLI。

## 证据与限制

- 全仓 race：497 PASS / 5 SKIP / 0 FAIL，日志 `.artifacts/rules-load-v026/go-race.jsonl`。五项跳过为两项 symlink 权限、原两项真实 CLI opt-in及新增规则集成 opt-in。之后新增的“资格核验后/实际事务前出现 override” focused race 回归通过，未重复全仓。
- 显式真实 CLI race 集成通过，日志 `.artifacts/rules-load-v026/local-rule-integration.log`：候选 AGENTS.md 被列为指令来源；不存在文件时无来源；AGENTS.override.md 优先级负向对照正确。没有访问用户项目或发送模型请求。
- 13 组前端、全部 JS 语法、Demo58、go vet、Windows 构建、Linux/amd64 交叉构建通过；Linux 未运行，远端 CI 未触发。
- CLI/API/内置浏览器使用 `.artifacts/rules-load-v026` 独立项目：确认/取消、实际安装两技能及一个规则文件、回退后的原 BOM/CRLF 手写文件逐字节一致、撤销后的整个项目逐字节一致、整个 .aidock 源保留。同目录 override 阻断且测试后已移除。重启读取原检查及三条恢复链，680px 无溢出，最终 error 日志为空。
- 负向覆盖默认不启动规则会话、缺 probe、未知版本、Claude 未支持、缺/重复/其它来源、检查中候选变化、错误输出不泄漏、超预算、源并发变化、旧规则/旧检查、遮盖变化。规则独用而无技能也可单独验证并产生可恢复文件计划。

这是空配置独立候选的指令来源证据，**实际项目 L3/L4 仍为 UNKNOWN**。它不证明模型逐字读取或遵从正文，不覆盖用户/企业配置、父目录和嵌套规则的真实优先级，也不使旧会话自动加载。文件安装不是脚本执行授权。网络/权限隔离治理仍按原门槛，不以进程生命周期控制替代。

官方契约：[app-server 的 instructionSources](https://developers.openai.com/codex/app-server/)；[AGENTS.md 的发现顺序与字节预算](https://developers.openai.com/codex/guides/agents-md/)。本地行为另由上面的精确版本对照验证，不从最新文档推断其它版本兼容。

截图 `.artifacts/aidock-rule-loading-v0.26.png` 与 `.artifacts/aidock-rule-loading-v0.26-narrow.png`；验收摘要 `.artifacts/rules-load-v026/acceptance.json`。未访问或修改 E:/work、E:/work2–5。

## 下一项

继续规则手改的显式导入/覆盖/协调，随后补规则文件库、Profile 持久关联、完整依赖/兼容、两工具实际会话与能力证据、隔离/凭据/审计、外部生成器执行/正式副本发布及 1E，再推进阶段 2–5。本增量不代表阶段 1 完成。

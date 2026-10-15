# Claude Code 规则来源检查 · v0.30.0-dev

日期：2026-10-15。继续冻结设计 v0.24 的阶段 1C；阶段 1 和整体 Goal 尚未完成。

## 当前行为

Claude Code 2.1.257 现在与 Codex CLI 0.162.1 一样，可以在用户显式确认后核对独立临时会话的规则文件来源。技能发现和规则来源分别检查；规则通过后，允许在同一持久事务中安装完整技能包和 CLAUDE.md/AGENTS.md 托管区块，支持联合回退、撤销和失败恢复。检查仍绑定候选字节、程序、适配器、配置层和目标观察；变化后旧证据不能复用。

界面入口仍为「AI 工具 → 验证规则加载」。CLI 使用 `tool check --all --probe --rule-probe`，API 使用 `/api/native/check` 的 `probe:true,rule_probe:true`。默认文件检查不启动进程，技能发现检查不会自动提升规则资格。两工具精确版本以适配器白名单为准。

## 协议与输入边界

Claude 使用官方 `@anthropic-ai/claude-agent-sdk@0.3.300` 类型声明中的 `get_context_usage` 控制协议，明确选择 `detail:summary`，读取本地上下文摘要的 `memoryFiles`。不使用可能请求 token-count API 的 full 模式；只发送 initialize 与 get_context_usage 两个控制请求，不发送 user/model turn。

新建独立项目和空工具 home；仅启用 project 设置来源，关闭 hooks、工具、MCP 和会话持久化，不继承调用者凭据。子进程的 API 地址额外设为不可用的本机回环端口，防止意外 token-count fallback 指向远端。此设置不构成 OS 网络隔离证明。沿用现有 Windows suspended/job 生命周期清理；超时 20 秒，输出和单行长度有上限。

解析器要求初始化成功且顺序正确，拒绝重复初始化、缺失来源列表、错误字段类型、非 Project 来源和过大响应。最终来源必须是唯一的候选 CLAUDE.md，检查前后候选字节一致；原始响应和未知来源正文不写入报告。

Claude 的 `@file` 会展开文件。尚未具备受控导入的完整批准输入契约，因此完整候选中出现任何 ASCII `@`，包括手写前后缀和邮件地址，都会保持 `UNKNOWN / RULES_IMPORTS_UNSUPPORTED`，不启动规则会话，也不授予安装资格。用户可以先把导入内容展开为已保存正文。此限制不阻止共享源编辑；完整导入管理继续开发。

来源通过只证明空配置临时会话报告了该规则文件，不证明实际项目配置优先级、旧会话重载、完整正文遵从或模型能力。L3/L4 继续 UNKNOWN。

## 已取得的验证

- 全仓 race：537 PASS / 6 SKIP / 0 FAIL，14 个测试包；日志 `go-race-final.jsonl`。新增显式 Claude 集成在默认套件中 SKIP，另行真实执行通过。13 组前端状态、58 项 Demo、JS 语法、vet、Windows 构建和 Linux/amd64 交叉构建通过；Linux 未运行。
- 真实 Claude Code 2.1.257 在独立临时目录完成空规则、单个 CLAUDE.md、另加 .claude/rules/additional.md 的对照，分别返回 0、1、2 个来源；真实 Check 和可安装 Plan 通过，没有模型请求。
- 协议单测核对仅有两个本地控制请求，以及不完整、乱序、拒绝、非候选来源与超限响应。两适配器的缺失、重复、未知来源、探测中改字节、协议错误、规则超预算和未支持版本均核对具体拒绝原因，不能获取安装资格。
- 内置浏览器在仓库独立体验项目完成两工具真实检查、四目标安装、联合回退和撤销。回退恢复 BOM/CRLF 手写原字节，撤销恢复完整安装摘要；全局库、项目源和本机覆盖未变化。
- 手写前缀加入未批准 @ 导入后，Claude 规则保持 UNKNOWN，界面安装入口关闭；恢复候选原字节并重启，旧检查显示过期，重新检查两工具通过。三条工具事务持久保留。
- 390×844 窄屏 document scrollWidth 与 clientWidth 均为 390。最终页面 console warn/error 为零。截图、磁盘摘要、CLI 报告和复核脚本位于 `.artifacts/experience-claude-rules-v030/`。

首次全仓 race 的运行器在部分输出后停滞，已终止，不计为通过；重新运行完整默认套件并得到 exit 0，以上数字仅统计完整新日志。原生拒绝原因加强断言也单独通过聚焦 race。

## 剩余必做

继续逐副本规则目标协调与联合恢复、实际项目 L3/L4、OS 网络隔离与出口 broker、凭据与审计、受管 MCP、外部生成器执行、正式五副本发布及 1E。之后继续阶段 2–5。本轮未访问五个真实工作目录。

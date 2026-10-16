# 技能、规则与 MCP 联合应用 · v0.65.0-dev

所选工具的完整技能包、规则托管区块、有效 Profile 中全部已授权 MCP 连接及字段归属，现在可一次预览、确认、失败恢复、回退和撤销。界面位于 AI 工具 → 联合应用技能、规则与 MCP。原技能/规则范围与单连接流程保留。

## 使用

先在 MCP 管理中完成安装登记、独立项目授权及运行时核验。联合安装不会授予权限，也不会启动插件。

1. 点击“① 检查技能与规则”，确认后在独立空配置目录验证所选工具的技能发现和规则来源。
2. 点击“② 读取 MCP 与联合历史”，核对当前全部有效 MCP 的已有安装、授权和运行时记录。
3. 点击“③ 预览联合应用”，逐项核对目标文件与 MCP 名称，再确认。取消不写文件。

CLI 契约：

```text
aidock tool prepare --all --application-scope skills-rules --probe --rule-probe <绑定参数> --json
aidock tool profile review <绑定参数> --json
aidock tool profile plan --file <联合请求.json> <绑定参数> --json
aidock tool profile apply --plan <UUID> --digest <摘要> <绑定参数> --json
aidock tool profile history|recover <绑定参数> --json
aidock tool profile rollback-plan --transaction <最新成功事务> <绑定参数> --json
aidock tool profile rollback-apply --plan <UUID> --digest <摘要> <绑定参数> --json
```

联合请求包含 native_plan、native_digest 和 sessions；sessions 必须明确列出有效 Profile 的全部 MCP，使用 review 的现有审查绑定，不接受启动标志或自定义 command。API 为 /api/native/profile/review、plan、apply、history、recover、rollback/plan、rollback/apply；读取使用 GET，变更/预览使用带 CSRF 的 POST。

## 一致性与恢复

源、配置层、安装产物及全部所选授权的锁保持到一次短文件事务完成。沿用原有版本/安装/权限/运行时门槛；任意项遗漏、过期、撤销或文件漂移都阻断。提交前再核对工具与 AIDock 程序、检查期限、源和目标摘要，不自动下载或启动插件。

所有目标共用一条持久批次事务和项目级互斥标记。两工具通常为七个根：两个技能目录、两个规则文件、JSON/TOML MCP 文件和连接归属。失败整批恢复，恢复前先核对所有根；任一人工修改导致 RECOVERY_CONFLICT，保留现场。重新打开控制台可从联合历史恢复，不重放工具或插件。项目诊断也识别此类待恢复事务。

MCP 字段复用现有无损合并器。联合集合移除所选工具中已停用的旧托管连接，保留未托管项和未选工具；同名未托管项不自动接管。有效 MCP 集合为空且原文件缺失时，缺失状态进入计划核对，但不会额外创建空配置文件。原始 JSON/TOML 只进入受限本机备份区，技能/规则的不可变备份复制到同一区域后共同提交。源和页面草稿保留。回退/撤销若重新引入多个启动器，在同一授权锁内重新核对全部原绑定。

## 精确版本兼容

本机 Codex CLI 已更新到 0.163.0，初次联合准备被原有 0.162.1 门槛正确阻断。按 [官方 App Server 文档](https://learn.chatgpt.com/docs/app-server) 核对 skills/list、thread/start 与 instructionSources，并以独立临时项目直接测试：精确包发现、临时指令来源、缺失文件负向与 AGENTS.override.md 优先级全部通过，随后才加入精确版本名单。文件格式合同未变，保持适配器合同版本与旧字段归属兼容；旧预览仍因完整适配器内容摘要变化而过期。无 turn/start 或模型请求，不提升实际技能 L3/L4。

## 验收与边界

专用项目含一个有效技能、规则、两个独立授权的自有 Node MCP、手写 JSON/TOML 与规则。真实 Claude Code 2.1.257/Codex CLI 0.163.0 的发现和隔离规则来源通过。CLI/API 的七根预览一致，遗漏 MCP 拒绝、实际共同安装、原字节回退、多启动器授权核对后的撤销、重复编译零差异和目标外改阻断通过。查询及安装未新增插件会话，源与产物树保留。

单元与进程故障测试验证后续目标失败的整批恢复、二进制附件、停用连接协调、受限备份、真实进程退出后的恢复、人工冲突零部分恢复及幂等恢复。原有授权/会话兼容与受影响包竞态回归通过；最终前端为 30 组状态/30 个 JS 语法，另有 Demo 58 项回归。Windows 构建、go vet 与 Linux 交叉构建通过；Linux 未运行。最终浏览器与原字节核对详见 verification.json 汇总的本机证据目录。

本机证据根为 `.artifacts/experience-profile-application-v065/`；最终构建使用全新 release-fixture 项目。初次工具版本阻断和中间构建证据保留，不能与最终构建混算。所有开发验证都未访问真实 E:\work、work2–5。

仍未完成：任意 plugins/config_overrides 等 Profile 字段适配、主应用入口整合、完整 Profile 的正式副本同步、通用隔离运行、外部生成器执行/发布、真实技能会话 L3/L4、项目 replay 和审计分段。阶段 1 总验收与完整 Goal 保持未完成；本增量只完成上述已支持组件的联合事务。

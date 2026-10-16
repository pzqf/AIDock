# 项目级原生 MCP 连接安装与恢复

版本：v0.58.0-dev。Claude Code 与 Codex CLI 的项目 MCP 配置现在支持预览安装、移除、回退、撤销回退及中断恢复。字段归属与两份配置共同提交；未知归属和外部修改会阻断覆盖。此增量不代表阶段 1 或完整 Goal 完成。

## 使用流程

管理界面从“AI 工具 → 安装 / 管理 MCP 连接”直接进入安装区域，也保留项目权限页入口。先完成当前插件的锁、安装、项目授权和运行时核验，选择工具，显式核对精确版本，再预览并确认。取消不写文件，用途草稿保留；按钮共用忙态，选择或输入变化会清除旧预览。

只安装 AIDock 生成的独立 command/args，不接收自由命令、环境变量或任意配置路径。安装不启动插件；新的客户端会话可能自动启动已授权插件。Claude Code 仍需其原生项目 MCP 批准，Codex CLI 仅加载受信项目配置；AIDock 不代替这些批准或授予原生项目信任。

固定项目文件为 Claude 的 `.mcp.json` / `mcpServers`、Codex 的 `.codex/config.toml` / `mcp_servers`，名称为 `aidock-<plugin>`。保留其它字段和手写字节。归属侧车 `.aidock-mcp-ownership.json` 绑定项目、作用域、副本、源目录、适配器合同、字段摘要和原启动器输入。名称前缀、注释或相同字节都不能授予未托管项所有权。全部已登记字段先检查漂移。

## 事务与备份

两工具文件和字段归属使用一个持久 Batch。与技能/规则事务共享实际项目锁和未完成事务屏障；逐文件可见，恢复保证不能被称为多文件原子可见。归属编译使用与事务 before-image 相同的不可变快照，确认时重新生成并比对全部目标。

原文件可能含用户手写凭据，完整原字节仅进入专用 `local/native-mcp-private/<binding-hash>/`，不进入普通 source blob、导出或 API 正文。创建及复用前先限制权限：Windows 保护 DACL 只含当前账户和 LocalSystem，并由新子文件继承；Unix 为 0700。不是加密，也不防御当前账户改写存储。

回退和撤销均需独立预览/确认，并恢复原字节与侧车存在状态。恢复旧启动器前重新核对原项目授权、安装、运行时回执和配置绑定；撤销授权后不能借回退重新启用旧启动器，须重新审查并安装。授权失效不妨碍明确移除当前受管连接。中断恢复只处理事务，不重放插件启动或业务调用；外部编辑冲突时先完整检查所有目标，再决定是否恢复。

## 版本与真实客户端

独立 `tool mcp versions --probe` 只在空临时项目、独立 home、有限环境中执行所选程序的 `--version`。不读取项目配置、不启动 MCP、不发送模型请求。记录绑定当前项目副本、程序摘要和精确版本，24 小时有效；最新失败不能沿用旧成功，安装预览及确认都重新检查程序摘要。

本机 Claude Code 2.1.257、Codex CLI 0.162.1 能读取实际生成的项目配置。Claude 的 `mcp get` 显示项目批准待完成，没有据此宣称连接成功。Codex 使用独立 home、明确受信的专用 Git 测试项目，通过真实 app-server 临时会话发现受管工具、读取 Public 结果，并拒绝跨项目调用。没有发送模型 turn，没有创建用户桌面聊天，不能据此提升技能 L3/L4。

真实 Codex 验收发现并修复两处协议兼容缺口：允许上游协商另一个已实现版本，客户端提出未知版本时选择已实现版本；标准 `_meta` 先验证为对象，再丢弃，不转发客户端 metadata。未知上游版本、畸形元数据、未知字段、未知/写工具和越界调用仍拒绝。

## CLI 与 API

CLI 位于 `aidock tool mcp`：`versions`、`plan --file`、`apply --plan --digest`、`history`、`recover`、`rollback-plan --transaction`、`rollback-apply --plan --digest`。版本检查需显式 `versions --probe --all` 或 `--probe --tool TOOL`。

安装请求包含 `action: install`、`tools`、最新 `check_id` 和当前 session 名称/权限摘要/审查摘要/运行时回执；移除仅需 `action: remove`、工具与 session 名称。所有操作沿用显式项目、作用域、副本、源、原生项目根和 home 绑定。

API 为 `/api/native/mcp/history`、`/versions` 的 GET，以及 `/versions/check`、`/plan`、`/apply`、`/recover`、`/rollback/plan`、`/rollback/apply` 的 POST。保留会话认证、CSRF、Origin、方法和大小限制；原文严格解析拒绝重复键、大小写别名、null、未知字段和自定义命令，不回显被拒绝值。

## 验收与剩余工作

证据目录：`.artifacts/experience-native-mcp-v058/`，汇总 `verification.json`。

八包完整竞态退出 0：privatefs、native、mcprelay、mcpsession、source、workstore、cli、console。工具首段输出有截断，不声称精确总用例数。最终归属快照与能力说明修改后，native/source 的 MCP 和 Configuration 定向竞态退出 0。覆盖两工具原字节保留、幂等、未托管碰撞、漂移、过期计划、篡改重算摘要、撤销后移除/禁止恢复旧启动器、第二目标失败、私有备份及真实 Windows DACL/子文件继承。

进程测试实际退出测试子进程，覆盖 prepared、staged、首目标完成、恢复中再次退出，以及外部编辑冲突；重新打开相同绑定后恢复全部配置与侧车，重复恢复为零。共享项目屏障阻止另一原生绑定在未完成时写入。

最终新建独立夹具的 15 项 CLI/API 验收退出 0，源和产物全树保持，原值只存在私有备份区。首次“最终夹具”因误改测试脚本换行转义导致插件语法错误，命令失败，不计通过；恢复原始正确模板、先检查插件语法、再用另一个全新夹具完整重跑通过，保留两轮日志。

26 组前端状态测试及 26 个 JS 语法检查通过。内置浏览器完成两工具状态、幂等禁用、取消、用途草稿、版本检查、预览后外部编辑拒绝、实际移除、回退、撤销和重新安装，直接入口与长弹窗关闭栏另行复核。gofmt、go vet、Windows 构建和 Linux 交叉构建通过；Linux 未运行。

仍限已独立授权的自有可信本地 Node、固定当前项目/Public 只读合同、同绑定单实例、Tier 0 非隔离。空闲撤销不主动轮询。原生 MCP 与通用技能/规则 apply 仍是独立流程；启用 MCP 的通用预检与联合应用尚需接通。多实例、逐次写批准、Tier 1/2、出口 broker、外部生成器实际执行、正式五副本发布及阶段 2–5 继续开发。没有访问或修改 E:/work、work2–5。

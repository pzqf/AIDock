# AIDock

面向多项目、多 AI 工具的开发环境配置管理工具，包含 Go CLI 与本机管理界面。

当前 v0.79.0-dev：已登记 MCP 的插件开关与字段配置可分层管理，并按精确授权交付给实际进程；支持默认值、空数组、null 和显式删除。界面可编辑、预览、保存及回退，已应用版本与后来草稿分别保留。Goal 进行中，阶段 1 总验收尚未完成。

进度按 [验收台账](docs/AIDock-验收台账.md) 的 59 条条件核对，不能以开发版本数量推断完成度。历史见 [开发进度](docs/AIDock-开发进度-2026-10-13.md)，本次见 [v0.79 实施记录](docs/AIDock-插件配置交付与分层开关实施-v0.79.0-dev.md)。

## 启动

需要 Go 1.25+；联合应用快照还需本机 Git。从仓库运行：

```powershell
# 打开项目工作台，在页面选择或创建独立测试项目
go run ./cmd/aidock config --home .artifacts/my-aidock

# 体验技能管理：首次创建示例，后续保留修改
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\start-demo.ps1

# 独立五副本体验
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\start-replica-demo.ps1
```

终端需保持运行；页面可关闭控制台，或用 Ctrl+C 停止。脚本支持 `-NoOpen` 仅输出 URL，便于内置浏览器打开；`-InstanceName my-test` 创建新的独立实例。体验数据和恢复点在仓库 `.artifacts/`，重复启动保留修改，初始化中断不会覆盖残留数据。

**E:\work、E:\work2–5 是真实工作目录，只可只读参考，不能用于写入、同步、恢复或测试。** 当前开发验收使用专用项目。设计文档和 Demo 不代表已经允许写入这些目录。

## 当前可用流程

| 入口 | 可以做什么 |
| --- | --- |
| 项目与技能库 | 独立项目绑定；完整包创建、导入、附件编辑、启停、版本继承/覆盖/分叉、搜索筛选；源保存预览、回退与撤销 |
| Profile 与规则 | 全局默认、extends、项目共享、本机覆盖与临时层；技能/MCP 三态组合和来源；已核验 MCP 的字段配置与逐进程交付；有序 Markdown 规则库 |
| AI 工具 | Claude Code/Codex CLI 完整技能包与规则检查、安装预览、确认、手写保护、回退/撤销和中断恢复；可选“仅技能与规则”范围，或共同安装全部已授权有效 MCP 连接 |
| MCP 定义 | 固定版本定义、安装锁、完整本机包导入、来源/评估证据、安装登记、SBOM、独立项目授权与撤销；自有可信 Node MCP 的锁定握手、Public 查询中继及项目原生连接 |
| 跨副本 | 登记、三方比较、逐项选择、当地生成、候选检查；内置 owned 技能与规则的正式同步、整批恢复、所选成员回退与最新撤销；本机配置排除 |
| 项目诊断 | 当前文件、历史发现证据、配置来源、本机覆盖、副本及待恢复事务汇总，跳转处理 |
| 安全审计 | 五类本机链完整性与范围、风险放行记录、授权/运行时/会话事件搜索和分页 |
| 密钥与时间线 | Windows 系统凭据库、独立轮换/引用检查；源及各类独立事务历史和恢复；联合应用完整源归档、字节核验、历史重建/依赖检查、两层草稿恢复及九根共同回放/回退/撤销 |

先读 [技能管理入门](docs/AIDock-技能管理入门.md)。保存源、安装工具文件、发布副本分别预览确认。源启用不代表工具已加载；文件一致、隔离发现、新会话加载和能力验证分别记录，未完成的 L3/L4 保持未验证。页面未保存草稿在关闭弹窗时保留，刷新页面会丢弃页面草稿。

## 当前限制

- 已支持组件可联合应用；插件配置只支持已登记 MCP、已核验字段清单和程序明确支持的 env-json-v1 合同。其它插件类型、第三方配置适配及完整 Profile 正式副本同步仍待完成；未知字段继续阻断。
- 外部技能生成器支持离线暂存交换，尚未开放执行及正式发布。内置 owned 技能/规则已支持正式发布。
- 真实技能新会话 L3/L4、Windows 网络隔离/出口 broker、通用插件运行、审计分段/外部可信锚点及完整 Profile/依赖恢复尚待完成。已有自有 Node MCP 路径是明确选择的 Tier 0 非隔离，只支持固定 Public 查询合同。
- 记忆集成、联网市场、跨机迁移与管理网关、自动沉淀等阶段 2–5 仍待开发。预留命令返回 `FEATURE_NOT_IMPLEMENTED`/4。
- 当前真实工具适配按精确版本验证；缺少条件不会提升生效证据。Linux 仅有交叉构建与既有局部证据，不能据此声称本轮运行验收通过。

## CLI 与验证

用 `aidock --help` 查看入口。已登记项目可用 `--home <数据根> --binding <入口ID>`；手动项目绑定使用 `--home`、`--managed-root`、`--project`、`--scope`，工具和副本流程还需 `--native-root`、`--copy`。具体参数以命令帮助及实施记录为准。

```powershell
go run ./cmd/aidock version --json
go run ./cmd/aidock skill scan --manifest examples/fixture.scan.yaml --json
# 下面两项追加上述项目绑定参数
aidock doctor --project-status --json
aidock audit --category install --page 1 --json
aidock tool applied --json
aidock replay <项目快照ID> --json
aidock replay <副本批次ID> --copies <copy-id1,copy-id2> --json
aidock replay <同一节点ID> --plan <提案ID> --digest <摘要> --confirm --json
aidock snapshot list --json
aidock snapshot check <快照ID> --json
aidock snapshot assess <快照ID> --json
aidock snapshot replay review <快照ID> --json
aidock snapshot replay permission plan --file <候选授权请求.json> --json
aidock snapshot replay permission apply --plan <计划ID> --digest <摘要> --json
aidock snapshot replay runtime state <快照ID> --candidate <候选摘要> --json
aidock snapshot replay runtime check --file <核验请求.json> --json
aidock snapshot replay clients <快照ID> --candidate <候选摘要> --json
aidock snapshot replay prepare --file <完整候选检查请求.json> --json
aidock snapshot replay prepared <检查UUID> --json
aidock snapshot replay plan --prepared <检查UUID> --digest <检查摘要> --json
aidock snapshot replay planned --plan <提案ID> --digest <提案摘要> --json
aidock snapshot replay apply --plan <提案ID> --digest <提案摘要> --json
aidock snapshot replay reverse --transaction <最新共同事务ID> --json
aidock snapshot replay history --json
aidock snapshot replay inspect --json
aidock snapshot replay recover --json
aidock snapshot draft plan <快照ID> --json
aidock snapshot draft apply --plan <计划ID> --digest <摘要> --json
aidock snapshot draft history --json

go test -race -count=1 -timeout 20m ./...
node scripts/check-web.cjs
node demo/tests/regression.cjs
```

普通 CLI 支持结构化 JSON。审计坏链退出 5，证据不完整的工具检查退出 3；两者仍返回报告。静态诊断不会启动工具；显式探测、安装、授权、回退各有自己的确认和输入复核。未完成事务先恢复，不要移动或删除 home/恢复标记来绕过保护。

## 设计与记录

- [开发计划](docs/AIDock-开发计划.md) · [验收台账](docs/AIDock-验收台账.md) · [开发进度](docs/AIDock-开发进度-2026-10-13.md)
- [冻结设计 v0.24](docs/AIDock-设计文档.md) · [设计基线清单](docs/AIDock-设计基线清单-v0.24.json)
- [v0.63 项目诊断](docs/AIDock-项目诊断汇总与恢复导航实施-v0.63.0-dev.md) · [v0.62 正式同步恢复](docs/AIDock-正式同步恢复条件检查与任意副本发起验收-v0.62.0-dev.md) · [v0.64 安全审计](docs/AIDock-安全审计查询与验收台账实施-v0.64.0-dev.md)
- [Demo](demo/index.html)：冻结交互原型，数据与故障为页面模拟；不能代替磁盘事务、工具生效或隔离验收。

v0.23/v0.24 原件保存在 `docs/baselines/`。当前实施注记随开发更新，不覆盖冻结原件。所有历史版本实施记录继续保留在 `docs/`。

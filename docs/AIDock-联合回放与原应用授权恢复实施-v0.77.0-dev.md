# 联合回放与原应用授权恢复 · v0.77.0-dev

更新：2026-10-17。Goal active，阶段 1 与 S1-20 保持部分实现；本版不是完整产品封版。

## 行为

应用快照可经“审查候选 → 当前项目授权 → 固定运行时核验 → 全部工具候选检查 → 共同写入预览 → 明确确认”恢复到已适配的技能、规则和受管 MCP 配置。授权、版本检查、候选检查及预览均不写入受管项目文件。确认后，一次事务共同处理项目规范源、本机 Profile，以及七个原生容器/文件根。

回退会分别恢复事务前的已保存源/本机草稿和工具实际使用的配置。后来保存但尚未应用的草稿，不被当作原应用配置；记忆、会话、审计、凭据、项目 Git 与追加的不可变修订材料不随回退倒退。保留原生容器内无关文件和原来的手写 JSON/TOML/规则字节。

原授权、运行时或程序已变化时，不能重新启用旧启动参数。回退预览隐藏不可执行差异，并给出原应用的审查入口。用户对原合同重新授权、重新核验后，生成带新凭据的新提案；该提案保留原回退的全部前镜像、已保存草稿和成功事务引用，只刷新受管 MCP 接收字段。一次共同提交完成，不先恢复草稿再单独安装。

仅一个完整原应用视图可在本路径重新生成。涉及多个原视图、旧安装缺少固定绑定、缺失依赖/正文、未适配字段或外部手改时，明确阻断。普通回退在原权限仍有效时继续使用原记录；撤销回退同样核对当前权限。

## 入口

追加当前项目的明确绑定参数。所有写入都需要预览所得完整摘要：

```text
aidock snapshot replay plan --prepared <检查ID> --digest <检查摘要>
aidock snapshot replay planned --plan <提案ID> --digest <提案摘要>
aidock snapshot replay apply --plan <提案ID> --digest <提案摘要>
aidock snapshot replay reverse --transaction <最新成功事务ID>
aidock snapshot replay history
aidock snapshot replay inspect
aidock snapshot replay recover
```

原应用审查的 `restore` 描述固定包含 `plan_id`、`digest` 和 `applied_binding`，在 review/permission/runtime/prepare 全程携带。CLI 只读命令使用 `--restore-plan`、`--restore-digest`、`--applied-binding`，必须一起指定。来源、程序路径及根目录不能通过请求正文替换。

认证 API 对应 `/api/snapshots/replay/{plan,planned,apply,reverse,history,inspect,recover}`。界面从时间线读取共同回放记录；应用快照审查页完成候选检查后可预览共同写入。旧组件和副本事务仍使用各自的恢复入口。

顶层 `aidock replay <snapshot> [--copies]`、完整 Profile、插件版本并存与依赖自动恢复、多个原应用视图协调及副本批次适配继续开发；不以本版 scoped 入口代替冻结设计的全部契约。

## 事务与隐私

- 九根使用现有源/Local/原生项目锁，执行前后核对身份、全部输入、当前独立授权/版本/产物与程序摘要；中断标记同时阻止其它写入。
- 失败恢复全部已修改目标；遇到外改保留文件和恢复标记，检查页显示冲突，不能覆盖用户修改。恢复使用结构性身份检查，不依赖已过期的运行授权。
- schema 4 归档绑定完整共同事务与其真实提交记录，归档意图不能启动 MCP。物理已保存草稿与原应用冻结输入分别存证。
- schema 4 引用存入 `refs/aidock/replays/`，旧 schema 1–3 保留原命名空间；旧版本不应被迫解析未知 schema。
- 原生手写内容只进入受限原生/共同缓存；规范源和本机覆盖进入各自恢复材料。CLI/API/UI 只返回身份、路径、字节数和摘要，不返回原生私密正文。

## 验收

证据目录：`.artifacts/experience-joint-replay-v077/`。七包完整回归、六包定向竞态、36 个 JS 语法/37 组状态测试、Demo 58、vet、Windows 构建和 Linux 交叉构建通过。最终二进制的 CLI/API 严格请求与只读等价、旧 v0.76 二进制的命名空间兼容，以及内置浏览器完整恢复链均通过；结果见 `verification.json`。Linux 未运行。

独立验收覆盖：独立真实双 MCP、两工具检查、完整共同应用、保存草稿后固定启动、回退、撤销、原合同授权替换后的新参数恢复、预览后撤权零写入、手写私密字段保留及 CLI/API 一致。浏览器另完成替换授权、两项固定版本核验、两工具隔离发现/规则来源、九根恢复预览/取消/确认及归档核对；恢复后真实 MCP 完成握手、Public 查询、越界拒绝和 EOF。390/1280 宽无横向溢出。项目源与 Local 精确匹配该事务的期望镜像，产物与全局库不变。测试夹具位于该证据目录；真实工作目录未访问。

未验收能力继续保持原边界：技能 L3/L4、Windows 网络隔离/出口 broker、通用插件执行、完整 Profile/外部生成器发布、审计分段与可信锚点，以及阶段 2–5。Linux 交叉构建不作为运行验收。

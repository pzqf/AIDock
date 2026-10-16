# 锁定插件握手与会话审计

版本：v0.56.0-dev。已安装、有效启用并独立授权的自有可信本地 Node MCP，可以显式启动锁内入口，完成限时初始化与可信工具清单核对。CLI/API/界面共用输入绑定、持久审计、进程清理和无重放恢复。持续客户端会话与原生配置接入继续开发，阶段 1 与完整 Goal 未完成。

## 启动与协议范围

只接受当前副本有效启用的 stdio 定义：完整产物、安装登记、项目授权、独立工具确认、自有可信 Tier 0 选择及当前 Node 版本匹配记录必须同时有效。请求绑定刚读取的 permission SHA、review SHA 和最新 runtime attempt；较新的失败或中断版本记录覆盖旧成功。只执行锁内 Node 加锁内入口，不接收自由命令、参数或环境。

进程使用最小平台环境，按当前独立授权交付系统凭据引用；启动后清空父进程命令对象持有的环境。通过生产中继仅执行 initialize、notifications/initialized 和 tools/list，不调用业务工具。已审查 descriptor 必须完整匹配，未知工具过滤，缺少已审查工具返回 MCP_SESSION_TOOL_MISMATCH。上游私有 metadata、stdout 和 stderr 不直接对外或进入会话记录，只持久化固定码、计数和协议摘要。

进程上下文上限 12 秒，中继单请求超时 3 秒、检查客户端传输上限 10 秒。Windows Job / Unix processgroup 清理根进程及后代后 Wait。转发前、返回前及结束时重新检查源、有效配置、权限树与完整产物。

这仍是 Tier 0 非隔离执行。文件、网络、子进程声明不构成 OS 访问限制，不适配任意第三方插件。整个有界检查持有 source → profile → artifact → permission 锁，不提供持续会话期间的实时撤销；持续会话实现必须采用短锁与逐次复核，不能直接延长本次锁的持有时间。execution_ready 保持 false，不提升 AI 客户端 L3/L4。

## 持久审计与恢复

Local Root 中的 mcp-session-<bindinghash> 和 mcp-session-transactions 保存项目、作用域、副本与源目录绑定。副本身份必须明确。输入包括授权、完整产物与运行时绑定、入口、工具策略摘要和版本核验 attempt。

生命周期为 INTENT → RELAY 摘要 → COMPLETE。成功必须具有 initialize 和 tools/list 的转发/返回证据；审计链与已提交事务锚点拒绝篡改和截断，启动前预留 72 条审计容量。中断保留未完成状态，恢复只处理审计事务，不重放进程、不推断成功。授权、配置、版本证据变化或超过 24 小时使旧结果历史化。锚点仅在本机，不能防御同用户重写全部历史。

## CLI、API 与界面

读取不会启动插件：

```text
aidock mcp session-check
```

显式限时握手：

```text
aidock mcp session-check --probe --plugin NAME --expected-permission SHA --expected-review SHA --expected-runtime-attempt UUID
aidock mcp session-recover
```

API 为 GET /api/mcp/session/state、POST /api/mcp/session/check 和 POST /api/mcp/session/recover。检查请求原文拒绝重复键、大小写别名、null、未知字段与自定义命令。完成失败时 CLI/API 均返回 ok:false、固定码和可操作中文说明；API 的结构化完成回执使用 HTTP 200。

项目权限页增加独立本次启动确认、执行中提示、成功/失败/未完成/历史结果及可展开绑定审计。发起请求前清空本次确认，避免重复请求或自动重试；失败也重新读取持久状态并刷新主诊断。恢复不会重新启动插件。主诊断分别报告观察到握手、过期、失败、未完成和审计恢复问题，不把握手结果当作完整运行资格。

## 验收

证据：.artifacts/experience-managed-session-v056/verification.json。七个受影响包完整竞态回归 386 PASS / 1 SKIP / 0 FAIL，退出 0；跳过为子进程 helper，不计通过。最后文案与反馈修改后聚焦 mcpsession/source/cli/console 及前端授权状态回归通过。25 组前端、25 个 JS 语法、gofmt、go vet、diff check、Windows 构建及最终 Linux 交叉构建通过；Linux 未运行。

独立测试项目沿用 20 项前置 CLI/API 验收，新增 13 项真实握手验收：只读无执行、过期输入拒绝、真实 Node 握手与清单、持久审计、版本变化历史化、严格请求解析、独立启动确认、API 等价、恢复无重放、撤销阻断、完整树保留及上游 metadata/父环境不进入会话审计。最终二进制再次通过 CLI 成功和缺少工具失败回执核对。

内置浏览器完成撤销状态禁用、旧结果历史化、独立重新授权、Node 版本核验、真实握手成功、确认清空及按钮禁用；失败项目在最终构建中实际执行并显示 MCP_SESSION_TOOL_MISMATCH 和修复说明，刷新出新失败记录。弹窗 869/869 无横向溢出，两页 warn/error 为 0。两项目完整源及产物树均匹配各自基线，不调用业务工具。Windows 专测实际系统凭据缺失阻断、授权修订交付及值不进入本机审计，并清理测试凭据。

未访问或修改 E:/work、work2–5。窄屏与静态 Demo 本轮未重跑。这些证据不证明实际 AI 客户端持续会话或 OS 隔离。

## 继续实施

下一增量接通实际客户端 stdio 持续中继、逐次权限复核及持久调用审计。原生 MCP 字段联合写入与恢复、Tier 1/2、外部生成器执行、正式副本发布及阶段 2–5 仍未完成；Goal 保持 active。

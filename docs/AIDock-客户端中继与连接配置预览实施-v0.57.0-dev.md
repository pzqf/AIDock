# 客户端中继与连接配置预览

版本：v0.57.0-dev。已授权的自有可信本地 Node MCP 现在可通过 AIDock 与真实 stdio 协议客户端连接，逐次复核当前授权及完整输入，持久记录调用摘要。提供 CLI/API/界面等价的 command/args 只读预览。原生客户端配置安装与恢复尚待接通，阶段 1 与完整 Goal 未完成。

## 使用与边界

客户端启动命令为 aidock mcp exec --allow-unisolated --plugin NAME --expected-permission SHA --expected-review SHA --expected-runtime-attempt UUID，必须提供当前项目、作用域、副本、源目录及显式 home 绑定。aidock mcp client-config 使用相同摘要生成独立 command 与 args 数组；不拼接 shell 命令、不启动、不写原生配置，不接受自由 command/env。程序需保留在预览的绝对路径，Local Root 必须对应显式 home/local。

只允许已安装、有效启用并独立确认的自有可信 Tier 0 本地 Node。完整产物、安装、项目副本授权、工具元数据/固定 Public 合同和最新 Node 版本回执均须有效。固定 scoped-public-query-v1 只读合同内的已审查工具可转发；未知或写工具、跨项目/作用域/副本参数及非 Public 返回继续拒绝。不是任意第三方 MCP 通用接入。

stdout 只用于 MCP 协议帧，启动、解析和 flag 错误仅向 stderr 输出固定安全码与通用说明，不回显参数值。内部映射请求身份，不传播上游私有 metadata；审计不保存原始客户端 ID、请求参数、业务返回正文或凭据值。

当前同一项目/作用域/副本仅一个受管实例，第二实例明确 SOURCE_BUSY。客户端 EOF 清理锁定进程及后代，记录 CLOSED；关闭管理控制台不终止独立客户端。单次协议请求超时 10 秒，持续会话没有握手检查的 12 秒总时限。

## 撤销、锁与审计

启动和每次请求/返回的实际传输持 source → profile → artifact → permission 短锁，不持这些锁等待上游响应。入口前 SOURCE_BUSY 最多重试 2 秒；进入回调后不重放启动、凭据交付、传输或审计副作用。审计写前竞争可安全重试，发生写入失败或容量不足先停止转发，预留完成回执空间。

上游挂起时可撤销、停用、改变产物或记录新的失败版本证明；返回前再复核并拦截原请求对应结果，清理会话。空闲时没有主动轮询撤销，撤销会在下一次协议请求或返回时生效，不能声称空闲进程即时退出。Tier 0 仍非隔离，声明的文件、网络和子进程范围不能构成 OS 限制。

生命周期 INTENT → RELAY 摘要 → COMPLETE。stdio 使用独立 mode，保留旧会话事件兼容。区分 CLOSED、REVOKED、CANCELED、FAILED 与启动/审计/清理失败；分别累计已转发读取、已返回读取、拒绝。EOF 不是 PASS，未完成只表述可能仍运行或已中断。校验 FORWARD/RETURN 的内部 request_id、method/tool 对应；恢复只处理审计事务，不重放插件启动或调用。锚点为本机事务，不防御同用户改写全部存储。

## 界面与 API

项目权限页增加“连接 AI 客户端”和“预览客户端连接配置（不启动）”，显示 command、独立 args 和当前限制。重读、选择/输入变化及授权变动清除旧配置；沿用草稿、忙态与恢复门槛。历史显示 stdio 关闭/撤销原因及三类计数，不把关闭当作全部调用成功，不提升 readiness 或 vendor L3/L4。

POST /api/mcp/session/client-config 使用严格原文请求解析、会话认证、CSRF 与 Origin 校验，重复键、大小写别名、null、未知字段及自定义命令拒绝。

## 验收

证据目录：.artifacts/experience-client-session-v057/，汇总 verification.json。

七包最终竞态合计 399 PASS / 1 SKIP / 0 FAIL。首次命令退出 1，仅两个旧“exec 未实现”CLI 占位断言失败；更新为实际绑定/授权门槛后，完整 CLI 竞态重跑退出 0，合计采用首次六个非 CLI 包和重跑 CLI，不重复计数。跳过是子进程 helper，不计通过。source 整包另行完整竞态回归退出 0，272.844 秒。

25 组前端、25 JS 语法、gofmt、vet、diff、Windows 构建和 Linux 交叉构建通过；Linux 未运行。沿用 20 项前置并新增 12 项独立 CLI/API：真实 exe/stdin/stdout、工具过滤与 Public 读取、越界/写/非 Public 拒绝、第二实例、EOF 计数、挂起时撤销、旧启动器失效、恢复无重放、API 等价/严格原文、关闭控制台后独立服务和完整树/私有 metadata 保留。首轮脚本重复执行前置夹具因入口保护缺失导致整体退出 1，修正 require.main 保护后完整命令退出 0，不归为产品通过的首次命令。

内置浏览器核对 command/args、配置预览无会话意图、stdio/撤销历史和计数、草稿/重读清空、实际撤销后禁用。弹窗 869/869，无横向溢出，warn/error 0。源及产物全树保持基线。窄屏和静态 Demo 本轮未重跑。

协议验收使用合成客户端与真实 Node 插件，尚非实际 Claude/Codex 客户端 L3/L4。execution_ready=false；未访问或修改 E:/work、work2–5。

## 下一步

接通两工具原生 MCP 字段的联合安装、字段归属和回退/撤销/中断恢复，并完成实际客户端验收。多实例、逐次写调用批准、Tier 1/2 与出口 broker、外部生成器执行及正式五副本发布仍未完成；阶段 2–5 按计划继续，Goal 保持 active。

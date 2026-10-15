# 自有非隔离选择与运行时版本核验

版本：v0.54.0-dev。接通受管启动的信任选择与版本观察前置；实际项目插件尚未启动，execution_ready=false。阶段 1 与完整 Goal 未完成。

## 信任与兼容

Tier 0 授权须单独确认这是自有可信本地插件、信任当前审查列出的锁定运行时完整字节，并接受非隔离边界。普通文件、网络、子进程权限或密钥确认不能替代本项。只接受 source.type=local；社区来源不因勾选而跳过 OS 隔离。文件、网络、子进程声明不构成实际 OS 限制。

授权输入增加仅 Tier 0 使用的可选 unisolated 元数据，绑定来源类型、运行时种类、版本、入口和 SHA256。记录增加 unisolated_own_trusted。旧记录缺字段仍可读取，但不会自动获得自有可信资格；旧非 Tier 0 序列化保持省略字段。撤销不需要新确认；恢复已撤销授权必须再次填写用途、独立确认并签发新 ID，旧核验不因此复活。

## 入口

mcp runtime-check 默认只读。实际核验必须使用 mcp runtime-check --probe --plugin NAME --expected-permission SHA --expected-review SHA。CLI 的 mcp runtime-recover 和 API 的 POST /api/mcp/runtime/recover 只恢复审计事务。

GET /api/mcp/runtime/state 读取当前副本记录。POST /api/mcp/runtime/check 仅接受 name、probe、expected_sha256、expected_review_sha256，拒绝重复键、大小写别名、null 和任意命令。已完成的失败结果在 CLI/API 均为 ok:false 并返回固定 code；API 的结构化执行结果使用 HTTP 200。

界面的项目权限页列出运行时、独立确认框、版本核验、历史及恢复入口。取消和重开保留草稿；成功保存清空用途与确认。核验及恢复后刷新主诊断，不能显示过时结论。

## 执行和审计

当前只核验显式信任的锁定 Node 稳定版本，固定 --version，不运行插件入口、不读取凭据、不接受自定义参数。Windows 基础环境由系统 API 取得 SystemRoot；不继承 PATH、HOME、NODE_OPTIONS 或用户密钥。5 秒超时，stdout 最多 128 字节，stderr 丢弃，只返回严格版本格式或固定错误码。

沿用原生探针的进程生命周期控制，抽出 processgroup 公共包：Windows 挂起创建、加入 Job 后恢复，退出清理后代；Unix 使用进程组。它控制生命周期，不是权限沙箱。

源、Profile 层、产物、当前授权保持锁定，执行前后核对完整产物，并在持锁后复核授权摘要。协议锁不阻止任意外部程序直接改写文件。版本返回匹配不能证明程序真正是 Node、可信发行者、插件成功运行、网络约束或安全等级提升；本机自算哈希继续 L0。

审计独立存于 Local Root 的 runtime-check-<bindinghash>，绑定项目、作用域、副本和源目录。执行前持久提交 INTENT，再启动固定命令，结束追加 COMPLETE；记录实际账户、授权/输入摘要和运行时元数据，不保存原始输出或凭据。独立 operation 锁覆盖两次审计事务及执行，避免存储锁重入。

审计链与本机已提交事务锚点拒绝篡改、截断及缺失；不声称防御同用户同时重写全部历史。中断保留 INCOMPLETE；恢复不会重放进程或推断成功。当前授权/输入变化或超过 24 小时后结果成为历史。

## 验收

证据：.artifacts/experience-runtime-check-v054/verification.json。核心五包竞态 127 PASS / 0 SKIP / 0 FAIL；source 定向竞态 84 PASS / 1 SKIP / 0 FAIL；原生生命周期 3 PASS。最后 source/cli/console 聚焦竞态 104 PASS / 1 SKIP / 0 FAIL，最终 console 完整竞态 39 PASS。跳过的 TestMCPArtifactExitHelper 是子进程入口，不计通过。各轮互有重叠，不相加为全仓数。

覆盖真实锁定 Node、最小环境、版本不符、过量/非法输出、超时和后代清理、审计失败不执行、panic 遮蔽、篡改/截断、中断意图/完成事务、新进程恢复无重放、撤销后过期和跨副本无记录。最初测试暴露夹具缺父目录及句柄释放延迟，修复夹具并使用有界释放检查后通过；原失败日志保留。

25 组前端回归通过。两次独立 CLI/API 各 14 项通过；最终二进制另完成真实版本不符、CLI/API 失败一致、主诊断 FAIL、源原字节回退、重新登记/授权并核验匹配。内置浏览器验收独立确认、预览取消与草稿、保存清空、真实 Node v24.18.0、撤销、恢复重新确认及旧结果历史；最终构建重跑核验，主诊断自动更新为 MCP_RUNTIME_VERSION_OBSERVED，弹窗宽度与滚动宽度均 869。

两个测试项目最终完整源和产物树匹配 baseline，插件入口标记不存在，动态父环境 sentinel 未持久化。gofmt、vet、diff check、Windows 构建与最终 Linux 交叉构建通过。Linux 未运行；窄屏与静态 Demo 58 项本轮未重跑。未访问或修改 E:/work、work2–5。

## 继续实施

可信工具策略、实际 stdio 受管启动与持久会话审计继续接通；不能用未知工具 annotations 代替可信声明。Tier 1/2 强制隔离、原生 MCP 字段联合写入/恢复、外部生成器执行、正式副本发布和后续阶段仍有必做，Goal 保持 active。

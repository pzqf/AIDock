# 回放候选运行时核验与连接预览 · v0.74.0-dev

更新：2026-10-17。Goal active，阶段 1 总验收及完整项目 replay 尚未完成。

在 v0.73 候选授权基础上，增加 CLI、认证 API 和时间线界面的固定运行时版本核验及完整候选连接预览。它们使用现有项目授权、运行时审计和受管 stdio 门槛。产品入口不应用源、本机覆盖或工具文件，也不启动插件入口或读取密钥。

## 用户流程与契约

时间线 → 读取应用快照 → 审查回放候选授权 → 选择 MCP。先完成候选的当前授权，再读取候选版本记录；勾选固定版本命令的执行确认后，才可核验选定 MCP。取消勾选不执行。全部启用项都满足条件后，才能预览全部候选连接；没有启用 MCP 时返回明确空集合。

在原有显式项目、作用域、副本、源、native-root、home 绑定后追加：

```text
aidock snapshot replay runtime state <快照ID> --candidate <候选摘要> --json
aidock snapshot replay runtime check --file <本次核验请求.json> --json
aidock snapshot replay clients <快照ID> --candidate <候选摘要> --json
```

版本核验文件是严格 JSON/YAML 对象：snapshot_id、candidate_digest、runtime；runtime 仅接受 name、probe:true、expected_sha256、expected_review_sha256。摘要从刚读取的候选版本记录取 permission_sha256、review_sha256。请求不能传命令、程序路径、候选正文、根或旧授权。拒绝重复键、别名、空值及未知字段。

认证 API 统一 POST application/json：

- /api/snapshots/replay/runtime/state：snapshot_id、candidate_digest。
- /api/snapshots/replay/runtime/check：上述核验请求。
- /api/snapshots/replay/clients：snapshot_id、candidate_digest。

沿用会话、CSRF、Origin、项目绑定、单次工作锁及受限请求长度。API 不接受用 JSON Content-Type 提交 YAML。核验完成但版本失败返回失败结果与真实事件，界面重新读取状态；状态重读失败保持不可继续，不沿用旧成功。

## 版本证据与连接生成

- 每次从不可变快照重建候选并比较完整摘要；当前项目源和 Local 草稿变化均使旧请求过期。历史冻结配置不重新继承今天的全局默认。
- 原 source/layer/artifact 锁、当前授权锁、固定 Node --version、精简环境和执行前后产物核对被抽成共享内部门槛，当前路径与候选路径使用同一实现。仅明确确认的自有本地 Tier 0 Node 能执行；未提升文件或网络隔离声明。
- 版本记录继续写原运行时审计，绑定实际新授权 ID/记录摘要和候选输入。候选 current 只相对于该精确候选；同一记录在不同当前源下仍为历史。读记录不新增执行审计。
- 全部启用 MCP 按名称确定顺序，选每项最新一次记录；失败、中断、过期、待恢复或属于旧授权的最新记录不能回落旧成功。仍受原有最多 24 小时有效期约束。
- 配置预览返回 command/args、绑定及 Probe=false 的会话选择，不写入原生文件。生成期间持有全部所需授权，逐项复核源/Local 及完整产物。实际 mcp exec 仍重复普通当前配置门槛。
- 草稿投影会将冻结共享输入投影到项目并清除 Local 中原全局关联，因此未来 argv 省略今天的 --library-root；不修改当前 Manager。真实投影后必须与普通 client-config 的参数一致。
- 权限撤销、更换、来源/安装/产物变化继续阻断。生成预览不复活旧权限，不创建第二套权威。

## 验证与证据

证据根：.artifacts/experience-replay-runtime-v074。Go 临时夹具覆盖真实固定 Node、未来参数与实际双根投影匹配、旧授权/最新失败/撤销/错误副本/摘要/源/Local 变化、缺确认、空集合及错误 home。8 个受影响包完整竞态、vet、33 个 JS 语法/34 组状态、Demo 58 项及 Windows/Linux 构建通过。Linux 仅交叉构建，未运行。

独立 acceptance/ 测试项目完成真实双工具联合应用归档和七根回退、候选审查/授权、CLI/API 版本与整组配置等价检查。候选参数在不同当前源下启动被拒绝；实际两层草稿投影后，两个自有夹具 MCP 均完成真实 stdio 初始化、合同过滤、Public 查询、越界拒绝及 EOF 清理。之后回退两层草稿，原项目源/Local/全局库/七个原生目标/两个完整产物树字节一致，权限/运行时/会话审计保留。此处实际运行的是本地自有测试插件，不能写成“全程没有执行插件”。

内置浏览器验证明确版本执行、撤回确认、整组配置预览、产物漂移拒绝并清空旧结果及恢复重读；390/1280 宽无横向溢出，warn/error 为 0。产物故障注入已恢复原字节。最终结果与构建 SHA 见 verification.json；早期界面排版构建另行保留，不冒充最终界面。

## 仍待完成

本次仅完成完整 replay 的候选版本与连接准备。源、Local、全部原生文件的共同提交、当前已应用配置与未应用草稿同时保护、重新编译与发现核验、故障/进程中断/人工外改恢复、顶层回退和撤销仍需实现，不能用“恢复草稿再普通 apply”替代共同事务。owned/override/external 版本材料、插件版本并存及独立/副本快照的适配仍未完成。59 条台账状态不提升，S1-20 保持部分实现。

E:\work、work2–5 未访问，未用于测试；未 push、部署或发送外部消息。

# 项目权限决策与授权恢复实施

版本：v0.44.0-dev。状态：实现、独立浏览器验收及最终受影响包回归完成；阶段 1 和整体 Goal 继续。

## 用户流程

在「MCP 定义 → 项目权限与授权」审查此副本已登记安装请求的文件路径、读写模式、网络出口、子进程及隔离声明，填写本次处理用途并明确勾选权限确认，预览后再保存。取消、关闭重开与过期错误保留用途草稿；重新读取发现审查输入变化时清除旧勾选，切换 MCP 也要求重新确认。

已保存记录可直接预览撤销，无需重新授予权限。历史回退与撤销回退均保留新审计；恢复任何已撤销或不同的授权，必须重新明确确认权限与用途，并核对当前依赖、签发新的授权 ID，不能复活旧授权身份。项目源定义或锁损坏时仍可撤销记录、恢复中断事务。

权限表格单独显示读写模式，项目与副本标识、账户 SID 及完整摘要收进详情；主视图保留实际源目录、作用域、账户名称与用途，避免核错项目。

## 契约与保护

- CLI：mcp permission state / plan --file / apply --plan --expected-digest / recover。
- API：GET /api/mcp/permission/state；POST /api/mcp/permission/plan、apply、recover。沿用控制台鉴权、CSRF、请求方法及严格输入检查。
- grant 请求要求 action、name、expected_sha256、expected_review_sha256、confirm_permissions=true、非空 purpose、data_level=Public、destination=unknown-external-client。revoke 仅使用 action、name 和已读授权摘要；rollback 使用 snapshot，恢复授权时还需本次确认与用途。
- 本机授权根绑定项目、作用域、副本 ID 和规范源目录；另一副本、作用域或源目录不能复用授权。当前源全树摘要和六层有效 Profile 摘要参与配置绑定，安装 ID/完整登记摘要、完整锁摘要、产物摘要、权限与 Tier 参与审查绑定。
- 仅与当前产物和安全证据一致的本机安装可建立授权。预备产物、失效安装、未知/失败输入不会自动获得授权。源、锁、本机层、安装或产物变化后旧计划失效，已有记录显示需要重新审查。
- 预览不改变有效授权；确认时重新读取并重建候选，核对计划摘要、当前账户、输入、历史和候选完整摘要，拒绝重放及重新计算摘要后的夹带文件。
- 授权、吊销、回退持续追加哈希链审计，记录实际 OS 账户与时间，核对本机已提交事务锚点；不宣称可防同用户管理员整体替换本地存储。回退保留既有审计，不抹除授权历史。
- 独立持久事务复用原字节备份与中断恢复。恢复不解析当前源锁；外部写入造成恢复冲突时保留现场，处理明确冲突后可重试。

## 能力边界

本轮只管理 Public 数据与未知外部客户端的项目权限决策，不支持 Internal/Confidential/Restricted 或特定远程目的地授权。记录匹配不代表隔离成立、运行资格通过或写调用已获允许；execution_ready 保持 false。诊断分别显示缺少授权、授权失效、授权当前和未完成的隔离/受管运行。

没有启动任何 MCP、运行安装脚本、注入凭据或修改系统权限。正式启动必须重新验证授权和运行门槛；未知工具、逐次写调用、目的地过滤、会话生命周期与 OS 强制能力仍待实现。

## 验证

初始全仓竞态退出 1：已记录 846 个通过和 8 个跳过，副本包累计超过 10 分钟超时，不能标为全仓通过。随后四包重跑的测试程序均输出 PASS（365 PASS / 1 SKIP），但 Go 父进程长时间未退出且已无子进程，终止该父进程，此命令同样不计通过。

将已暂存的精确 v0.44 源导出到独立目录后，以 -count=1 关闭测试缓存重跑 source、cli、console、replica 四包：365 PASS / 1 SKIP / 0 FAIL，4 包，退出 0；命令使用 20 分钟超时。保留全部原失败、中断和最终成功日志，未宣称最终代码全仓命令通过。

22 组前端状态、23 个 JS 语法、58 项 Demo、go vet、Windows 构建及 Linux 交叉构建通过，Linux 未运行。回归覆盖明确确认与数据边界、预览取消、重放、安装前拒绝、配置/本机层/锁/产物/安装变化失效、另一作用域与副本隔离、损坏配置吊销、审计篡改、重算摘要夹带、真实子进程 exit94 和外部写入恢复冲突。

独立内置浏览器完成预览取消、关闭重开草稿、授权、撤销、无确认恢复拒绝、明确重新确认恢复、撤销恢复、并发安装重登记使旧确认过期、草稿保留与重新勾选、最终构建重启保留 5 条审计和 1 项当前授权。390×844 最终检查页面 390/390、对话框 331/331、权限表格 258/258、用途输入 290/290，无横向溢出，warn/error 为 0。

脚本核对项目源 7 文件、产物 2 文件摘要不变，保留 1 个本机安装；execution_ready=false。所有测试位于独立 .artifacts 实例或临时目录，未访问五个真实工作目录。

证据目录：.artifacts/experience-mcp-permission-v044。包括 verification.json、go-race.jsonl、go-race-final-affected.jsonl、go-race-final-no-cache.jsonl、frontend-final.json、acceptance.cjs、acceptance.json、permission-desktop-final.png、permission-narrow-final.png、permission-stale.png。最终 Windows 验收构建为 aidock-complete.exe，最终重启与授权状态截图为 permission-complete.png。

## 剩余任务

继续 stdio 中继、逐次调用及非公开数据/目的地授权、出口 broker、OS 凭据库与隔离、会话生命周期和原生配置接入。联网获取与依赖解析、真实评估服务/在线刷新、MCP 定义/锁的副本同步、正式发布、L3/L4、阶段 1E 和阶段 2–5 尚未完成。

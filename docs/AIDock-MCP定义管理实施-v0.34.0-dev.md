# MCP 定义管理 · v0.34.0-dev

日期：2026-10-15。阶段 1C 的定义管理增量；阶段 1 与整体 Goal 保持进行中。

## 使用流程

打开或创建项目后，从侧栏进入「MCP 定义」。可以新增、编辑、声明启停和移除服务器定义，填写来源类型、包名/URL/项目相对路径、固定版本、安装目录相对入口及文件/网络/子进程权限。新定义默认停用、文件只读、网络默认拒绝、不允许子进程。

「预览保存定义」显示新增、修改和移除的定义，以及已保存/确认后的完整来源、版本、入口、Tier 和权限。候选诊断与已保存诊断分别显示。取消不写源，关闭弹窗保留草稿；重新读取需要再次确认放弃草稿。磁盘变化使预览/确认过期，草稿不被覆盖。

确认仅更新当前绑定源的 `.aidock/mcp.yaml`，保留完整源事务。时间线可恢复到保存前、撤销回退，或恢复中断的保存；回退作用于整个当前项目源，界面在确认前说明范围。BOM、CRLF 和注释的原字节进入恢复点。此流程不下载安装包、不执行来源中的安装命令或入口，也不写原生工具配置。

## 契约与诊断

沿用冻结设计的 `mcp.yaml` v1 字段，不新增直接 shell 启动配置。名称需能作为唯一的短标识，大小写重复被拒绝；入口必须为安全相对路径，拒绝逃逸、绝对路径和 Windows 设备名。语义检查补足原 Schema 无法表达的歧义/路径约束，不改变字段含义或提升执行资格。

受管接入只支持 stdio。HTTP/SSE 定义可留作待处理声明，但诊断固定返回 `UNSUPPORTED_TRANSPORT`；无直连回退。远程来源需要包名或 URL，以及精确版本/提交；浮动版本标为 `MCP_VERSION_NOT_PINNED`。本地来源的绝对路径或越界路径显示不可携带，定义管理不读取来源目录。

`plugins.lock` 缺失显示 `MCP_LOCK_MISSING`；发现文件也只显示 `MCP_LOCK_NOT_VERIFIED`，不能据其存在或本地自算摘要赋予信任。Tier 0 和默认允许网络分别说明声明边界；尚未实现安全门的最终等级判定。

`enabled` 只表达启用意愿。每项及总报告的 `execution_ready` 均为 false，授权、隔离和受管启动保持 UNKNOWN。已声明启用项的诊断进入源 doctor、Profile 检查及两工具预览，避免忽略 MCP 后仍声称整套配置可应用。没有定义时的 PASS 只表示无项可检查。

## CLI 与 API

```powershell
aidock mcp list <项目绑定参数> --json
aidock mcp check <项目绑定参数> --json
aidock mcp plan --file edit.json <项目绑定参数> --json
aidock mcp apply --plan <UUID> --expected-digest <摘要> <项目绑定参数> --json
```

编辑文件为 `{expected_sha256, config}`，config 是完整 mcp.yaml 对象。list 读取成功返回 0；check 存在静态失败返回 `STATIC_CHECK_FAILED`/5，存在未完成证据返回 `EVIDENCE_INCOMPLETE`/3。JSON 保留完整逐项结果。`mcp exec` 继续保持 stdio 输出干净并明确未实现。

API 为 GET `/api/mcp/state`、POST `/api/mcp/plan`、POST `/api/mcp/apply`。plan 接受同一编辑对象，apply 只接受 `plan_id` 和 `digest`。沿用固定项目绑定、会话鉴权、Origin/CSRF 与严格请求解码。MCP 专用确认拒绝其它源动作，保存计划再次校验定义且只允许改变 mcp.yaml。

## 验证记录

测试覆盖定义保存、重建管理器读取、完整原字节回退/撤销、源变化阻断、取消、坏文件的显式摘要修复、写入失败恢复、未知锁不信任、HTTP/浮动版本/本地越界诊断、CLI 退出码、API 鉴权与严格确认、doctor/原生预览不遗漏启用声明、前端草稿保留与无运行路径。

内置浏览器使用 `.artifacts/experience-mcp-v034-ui/` 独立项目，验收新增、预览/取消、保存、诊断、大小写重复、外部字节变化、草稿保留、时间线回退/撤销及重启读取。回退精确恢复第一份已保存定义，原四个技能及其它项目文件的摘要另行核对。390×844 页面和弹窗无横向溢出，重新打开从顶部开始；窄屏 MCP 导航保留明确可访问名称。

证据目录：`.artifacts/experience-mcp-v034/`。最终回归数字以 `acceptance.json` 和日志为准。首轮全仓测试有一次 Windows `openat` 系统资源不足，整体失败，保留为 `go-race-first.jsonl`；不能计为通过。旧缓存进程退出后，最终全仓使用 `-race -count=1 -p 2 -timeout 20m`，不降低产品检查、恢复或执行门槛。最后 CLI 退出码等调整另有五包聚焦回归。

完整 Windows race 正常退出 0：618 PASS / 6 SKIP / 0 FAIL，14 个包通过。最终五包聚焦 race 为 31 PASS / 0 FAIL；14 组前端状态、58 项 Demo、全部 JS 语法、gofmt、vet、Windows 构建及 Linux 交叉构建通过，Linux 未运行。最终浏览器 warn/error 为 0，390×844 页面宽度为 390/390、弹窗为 331/331，重开 scrollTop 为 0。原项目 12 个文件摘要不变，唯一新增为 `.aidock/mcp.yaml`，原生工具目录未创建。

## 下一项

继续实现安装锁与本地产物验证，随后接入 Profile MCP 组合、受管启动、授权/隔离/凭据审计、原生配置、会话与副本传播。当前未完成这些能力，也未完成阶段 1E 或阶段 2–5。五个真实工作目录未访问、未修改。

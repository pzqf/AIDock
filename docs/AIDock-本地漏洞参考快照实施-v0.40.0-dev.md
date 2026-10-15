# 本地漏洞参考快照实施 v0.40.0-dev

本增量提供当前 MCP 版本与 plugin.yaml 直接声明依赖的离线漏洞参考缓存，贯通 CLI、API 和管理界面。用户从独立渠道核对后显式导入完整快照；AIDock 不联网查询、不启动扫描器。覆盖完整且未记录漏洞仍保持 L0 / UNKNOWN，不是独立安全评估、完整传递依赖扫描或运行授权。冻结设计仍为 v0.24，阶段 1 和整体 Goal 未完成。

## 参考范围与有效性

快照绑定完整来源/产物/运行时 subject、包含权限的定义摘要和 manifest 摘要；仅接收当前版本及明确直接声明依赖。模板中的 advisories:null 表示尚未检查，不能保存；核对后明确填写 [] 才表示该参考未记录漏洞。保留参考来源、HTTPS 地址、采集/到期时间、包版本及每条公告详情，地址只保存和显示，不抓取内容。

状态分别为 MISSING、STALE、EXPIRED、PARTIAL、FOUND 和 NO_KNOWN_MATCHES。有效期最长 30 天；未来采集时间不能作为当前证据。已记录风险形成 FAIL 并阻断安装，过期不会自动解除已记录风险。产物、定义或权限变化使绑定失效，旧参考保留可查。缺独立来源认证、恶意代码分析、静态分析及身份核实的事实继续明确报告。

严格解析 JSON/YAML，拒绝未知字段、重复包/公告、未明确检查的 advisories、无效时间/绑定及带凭据、查询或片段的 URL；限制原文 2 MB、1000 包、2000 条公告。模拟公告只出现在独立验收夹具，不作为真实漏洞结论。

## 保存、回退与恢复

缓存绑定当前项目、作用域、物理源目录及 Local Root，不随共享源或副本同步传播。保存保留原文；预览不修改生效缓存，取消和关闭弹窗保留草稿。生成模板或载入旧快照不会覆盖已有草稿，项目切换也保护未保存内容。读取失败清除旧结果，避免继续展示旧通过结论。

导入、移除、整份缓存回退及撤销共用 workstore 事务、写前快照、完整树摘要与中断恢复。确认时重新核对源、当前产物、权限、时间和原缓存，并重建完整候选；改候选后重算摘要不能夹带文件。最新本机已提交 journal 的树摘要用于检测缓存外改；这是本地完整性锚点，不是外部签名存证。移除风险参考属于显式有记录的变更，不赋予更高信任等级。安全证据变化使旧安装审查过期。

恢复不依赖解析损坏的 portable MCP 锁，也不覆盖该锁。未完成事务阻断后续写入。回退旧参考仍按当前产物及时间重新判断，不会把过期内容变成通过。

## CLI 与 API

沿用明确 managed-root/home/project/scope/copy/native-root 绑定：

```text
aidock mcp security state
aidock mcp security check
aidock mcp security plan --file <请求.json>
aidock mcp security apply --plan <UUID> --expected-digest <摘要>
aidock mcp security recover
```

导入请求示例：

```json
{
  "action": "import",
  "expected_sha256": "<state 返回的缓存摘要>",
  "document": "<完整快照 JSON/YAML 原文>"
}
```

remove 使用 subject_sha256，rollback 使用 snapshot。快照格式为 aidock-vulnerability-reference-v1，schema_version 为 1，含 subject_sha256、definition_sha256、manifest_sha256、provider、reference_url、collected_at、expires_at 和 packages；每包包含 ecosystem/name/version/advisories。公告含 id/severity/summary/url。

state 读取成功返回 0；check 因独立安全证据未齐返回 3，已记录风险或缓存故障返回 5。没有定义时的无项结果不代表安全评估通过。API 为 GET /api/mcp/security/state，POST /api/mcp/security/plan、/apply、/recover；沿用会话、CSRF、方法、严格输入和绑定检查，apply 接受 plan_id/digest。

管理界面入口：MCP 定义 → 安装锁与本地产物 → 漏洞参考与安全检查。提供绑定模板、原文编辑、预览确认、覆盖/公告详情、原文、移除、历史回退/撤销和恢复。全部内容以文本显示，未认证参考不提升 L0，execution_ready 始终为 false。

## 验收证据

新增回归覆盖完整/部分覆盖、已记录风险、过期风险保留、未检查模板、严格输入/TTL/绑定、原文 BOM/CRLF、预览不写、重启、另一 home 隔离、回退/撤销、安装阻断与旧确认失效、源/产物/权限变化、缓存外改检测、确认重新核对 TTL、重算候选摘要夹带文件。真实子进程 exit 94 后由新 Manager 恢复，损坏 portable 锁原字节保留。CLI 退出码与 API 会话/CSRF/严格请求有对应回归。

20 组前端状态回归、58 项 Demo、21 个 JS 语法、vet、Windows 构建及 Linux 交叉构建通过；Linux 未运行。Windows 全仓竞态 779 PASS / 7 SKIP / 0 FAIL，18 包，进程退出 0；命令为 go test -race -count=1 -p 2 -timeout 20m -json ./...，未把包级通过计入测试用例。110 个相关文档相对链接检查通过。

内置浏览器在 .artifacts/experience-mcp-security-v040-ui 独立项目完成 MISSING、模板、预览无写、取消/关闭保留草稿、完整覆盖、模拟风险、回退/撤销、EXPIRED、PARTIAL 和恢复完整参考。重启后 2/2 覆盖、L0 和 7 条历史保留。390×844 页面 390/390、弹窗 331/331、编辑框 290/290，无横向溢出；脚本 warn/error 为零。13 个源文件和 16 个产物文件摘要不变，未创建原生目录、未登记安装、未运行插件。五个真实工作目录未访问。

证据位于 .artifacts/experience-mcp-security-v040：acceptance.cjs、acceptance.json、verification.json、go-race.jsonl、known-risk-desktop.png、expired-reference.png、reference-desktop.png、reference-narrow.png、reference-restarted.png。

## 后续

继续独立外部评估缓存与信任判定、安装获取、Profile MCP 组合、项目授权、OS 隔离、受管 stdio 与出口 broker、原生接入及计划其余阶段。本地漏洞参考不是完整 CVE 扫描或 L1/L2 安全评估完成证据。

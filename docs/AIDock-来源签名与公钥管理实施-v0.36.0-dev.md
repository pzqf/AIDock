# 来源签名与公钥管理 · v0.36.0-dev

日期：2026-10-15。冻结设计仍为 v0.24；本次落实 §9.1 的本地缓存来源证据与自建密钥验证前置，不把单份来源签名称为完整 L3 评估。阶段 1、1E 和整体 Goal 均未完成。

## 当前流程

在「MCP 定义 → 安装锁与本地产物 → 来源签名与公钥」管理当前项目本机证据：

1. 在独立渠道核对发布者公钥，登记名称、Ed25519 公钥 Base64、明确来源类型/标识及到期时间。预览显示完整 SHA-256 指纹、前后值及变化路径；取消保留草稿，确认才登记。
2. 在签名对象摘要中核对来源、版本、平台、产物摘要与 subject SHA-256。由发布者在外部签署声明，AIDock 不生成、索取或存储私钥。
3. 导入 JSON/YAML 签名声明，核对候选验签结果及声明前后原文，再确认缓存。可缓存未登记密钥或失效的声明，但其状态继续为 UNKNOWN/FAIL，不能复用有效结论。
4. 公钥停用或过期立即使旧声明失效；签名、来源范围、完整产物绑定或有效期不符分别报告。声明过期保留缓存。
5. 来源时间线可回退到某次保存之前，也可撤销回退；每次都保留已有审计并追加记录。中断恢复入口恢复未完成事务；外部修改冲突保留等待处理。

来源保存或重读会刷新安装锁和 MCP 定义诊断，保留公钥/声明、安装锁和定义各自的未保存草稿。切换项目时来源草稿也纳入保护。

## 数据与边界

`internal/pluginorigin` 使用标准库 Ed25519。公钥指纹为 `SHA256(decoded 32-byte public key)`；签名须为规范 Base64 的 64 字节 Ed25519 签名。所有输入严格拒绝未知字段、重复字段和非法版本。

公钥作用域精确匹配来源类型和来源标识，不支持通配信任。公钥绑定当前项目、scope、物理 Source Root 及 Local Root，不依赖可省略的 copy 命令行标记。相同源换一个 Local Root、换项目、scope 或源路径均不能继承信任；Config Root、副本候选和导出均不包含这些公钥。

来源对象摘要绑定来源类型/标识、产物精确版本、平台、入口、完整产物摘要、运行时类型/精确版本/约束/路径/字节摘要、解析锁和按路径排序的完整依赖。项目 alias、本机安装目录、定义摘要和权限选择不属于发布者声明，仍由独立定义/权限门槛绑定。签名不能替代磁盘产物逐字节核验。

声明格式由 `schemas/plugin-origin-evidence.schema.json` 限定：

```json
{
  "schema_version": 1,
  "key_id": "<公钥 SHA-256 指纹>",
  "payload": {
    "schema_version": 1,
    "kind": "artifact-origin",
    "subject_sha256": "<当前产物 subject SHA-256>",
    "publisher": "发布者名称",
    "release_url": "https://example.invalid/releases/1.2.3",
    "issued_at": "2026-10-15T00:00:00Z",
    "expires_at": "2026-10-16T00:00:00Z",
    "runtime_version_verified": false
  },
  "signature": "<Base64 Ed25519 签名>"
}
```

签名输入是 UTF-8 的 `AIDock artifact origin v1\n` 加 `json.Marshal(pluginorigin.Payload)`，不含末尾换行。Payload 按上例字段顺序序列化，包括所有布尔值；采用 Go 标准 JSON 转义（含 HTML 字符和 U+2028/U+2029）。外部实现须遵循此版本字节合同；Go 发布者可直接调用 `pluginorigin.SigningBytes`。原始声明的缩进/注释不参与签名，但缓存与回退保留导入原字节。有效期晚于签发时间且最长 90 天，时间均按 RFC3339；发行链接必须为无凭据、查询参数及 fragment 的 HTTPS URL，验签过程不访问它。

有效签名只证明登记密钥签署了此对象，发布者身份依赖登记时独立核对。`runtime_version_verified=true` 仅表示登记发布者的签名声明，不代表 AIDock 运行了 `--version`。缺少 manifest、SBOM、恶意代码/CVE 与静态分析等安全评估时，信任等级仍为 L0，`execution_ready=false`；不会直接授予 L2、项目执行权或 OS 隔离。

## 恢复与审计

本机目录为 Local Root 的 `plugin-origin-<项目/scope/源路径绑定摘要>`，事务使用独立 `plugin-origin` 种类，保存在 sibling `plugin-origin-transactions`。源锁先于来源锁；更改计划绑定全部当前源摘要、本机 before 树、确认摘要及候选有效性状态。来源、密钥、证据或有效期在预览后变化即拒绝旧确认。

证据目录只接受 `binding.json`、`keys.json`、`reports/<subject>.json` 与 `audit/<sequence>-<uuid>.json`。非空无绑定、其它项目绑定、未知文件、链接和审计链损坏均阻断接管，保留现状。keys 动作只准更改密钥表，evidence/remove 只准操作指定声明，rollback 必须等于已提交历史的 authority 内容；绑定不可改写。

每条审计记录包含序号、动作、时间、前一条原字节摘要及 authority 前后摘要。现存链与本机 committed 事务的审计内容核对，截断到旧前缀也不能自动通过。回退保留全部审计，仅恢复密钥/报告内容，再追加记录；单独外部修改 authority 时可显式从已提交历史修复。审计结构/锚点损坏不自动覆盖。

这套本机哈希链和事务锚点用于发现意外改动与回退截断，不是远程存证或对拥有全部本机写权限的攻击者的防护。

## CLI / API

所有命令沿用明确的项目绑定参数：

```powershell
aidock mcp origin state --json
aidock mcp origin plan --file origin-edit.json --json
aidock mcp origin apply --plan <UUID> --expected-digest <摘要> --json
aidock mcp origin show --subject <SHA256> --json
aidock mcp origin recover --json
```

编辑动作是 `keys`（完整 `keys` 表）、`evidence`（原字节 `evidence` 字符串）、`remove`（`subject_sha256`）或 `rollback`（`snapshot`），均需最新 `expected_sha256`，不能混带其它动作字段。恢复使用独立入口，即使便携安装锁格式损坏也可执行。MCP exec 的 stdio 合同不受这些普通 JSON 命令影响。

API 为 `/api/mcp/origin/state|show`（GET）及 `/api/mcp/origin/plan|apply|recover`（POST），共用 Source 后端、session/Origin/CSRF、严格输入解码和逐请求项目绑定。

## 验证证据

独立项目：`.artifacts/experience-mcp-origin-v036-ui/`。证据：`.artifacts/experience-mcp-origin-v036/`，包含完整竞态回归、来源补充竞态回归、CLI 验收脚本、原字节基线、公钥/声明虚构夹具、最终检查及浏览器截图。

浏览器已验证登记预览/取消/确认、真实 Ed25519 声明导入、停用失效、回退/撤销、过期和篡改候选、三处草稿保留。签名使用运行时生成的测试私钥，私钥未写盘。独立项目源和 2 个产物文件摘要保持不变，未创建原生工具目录。未访问或修改五个真实工作目录。

最终 Windows 全仓竞态回归正常退出 0：**719 PASS / 7 SKIP / 0 FAIL，16 包通过**；跳过不计作通过。最终恢复提示修正另有来源包竞态 **45 PASS / 0 SKIP / 0 FAIL**。16 组前端状态、58 项 Demo、JS 语法、gofmt、vet、Windows 构建、Linux 交叉构建及 98 个文档相对链接检查通过；Linux 未运行。首次全仓回归的原生测试仍断言旧诊断码，记录保留为 `go-race-before-native-contract.jsonl`；修正并扩充“有效签名仍不能越过安全/执行门槛”断言后完成上述最终回归。

重启后 7 条追加审计、公钥、声明和验签结果均保留；390×844 页面宽/滚动宽为 390/390、弹窗为 331/331，重开 scrollTop=0，页面 warn/error 为 0。最终界面见 `saved-origin-desktop.png`、`saved-origin-narrow.png`。原 11 个项目种子文件以及建立安装锁时新增的定义/锁文件，在来源管理验收期间摘要全部保持不变；2 个产物文件也保持不变。详见 `acceptance.json`、`verification.json` 与可复核脚本。

后续继续 manifest/SBOM、安全评估、安装获取、Profile MCP 组合、授权/隔离/受管运行与计划其余必做项，不以本增量作为阶段 1 或 Goal 完成。

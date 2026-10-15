# 独立评估缓存与信任判定实施 v0.41.0-dev

本增量把已独立登记的评估者公钥、外部签名报告和累积 L1/L2 判定接入 MCP 安全检查、安装预览及安装审计。达到默认 L2 可按标准流程登记；L0/L1 每次登记或恢复仍须明确放行与理由，任何已知失败继续阻断。冻结设计仍为 v0.24，阶段 1 和整体 Goal 未完成。

## 证据与信任边界

评估者与发行来源分别登记。来源签名公钥不会自动成为评估者；评估者公钥只对当前项目、作用域、物理源目录和 Local Root 生效，不随副本传播。每次公钥变更都要求独立核对确认与说明，保留公钥指纹、评估者名称、限定来源及核对地址。用户负责通过独立渠道核对登记对象；本机登记不是自动证书认证。恢复历史评估者也需要本次确认和说明。

外部报告使用 Ed25519 验签，签名字节为 `AIDock plugin assessment v1\n` 加 Go 结构体的紧凑 JSON。与来源签名分离的前缀防止把发行声明当成安全报告。签名证明已登记密钥签署了这些声明，不证明外部评估者的声明必然真实。没有内置通过签名、虚构评估服务或自动生成通过报告的入口。

报告绑定完整来源/版本/平台/产物/运行时/解析依赖 subject、定义及权限摘要、manifest 摘要，并明确覆盖完整产物、运行时与解析依赖。恶意代码检查、静态分析、发布者身份核实各自包含 PASS/FAIL/UNKNOWN、方法与版本、说明和证据地址；缺项不能自动当成通过。报告嵌入的漏洞参考覆盖当前版本及每项直接声明依赖，保留来源和时间。传递依赖检查由外部评估者声明的完整分析范围负责，core 不内置扫描器。

累积判定如下：

| 结果 | 必需的当前证据 |
| --- | --- |
| L0 | 未满足下列基础条件，或证据缺失、过期、绑定失效；具体原因单独报告 |
| L1 | 当前完整产物、有效 manifest/权限及明确直接依赖，可生成轻量 SBOM；有效来源签名与运行时版本声明；已登记评估者的有效签名报告，恶意代码检查通过、漏洞参考完整覆盖且未记录风险 |
| L2 | L1 加上完整解析依赖/哈希与直接声明核对、静态分析通过、发布者身份核实通过 |
| L3/L4 | 本增量不判定，继续显示未验证 |

报告与公钥最长沿用各自明确有效期，报告参考最长 30 天。未来采集时间和过期内容不授予当前等级；有效签名中的失败即使过期也保留。停用评估者使旧通过失效并阻断安装。签名无效、来源范围或署名不符不能导入为已确认评估。本地独立参考已记录的风险不会被另一份签名通过报告静默消除，其公告详情和数量继续显示。

L2 只代表当前已登记证据满足安装检查，不授予项目运行权限、凭据、网络或子进程权限。execution_ready 始终为 false。没有集成真实评估供应商、在线刷新、真实恶意代码/静态扫描，未声称对任意真实插件完成安全验收。

## 事务、回退与旧确认失效

沿用 v0.40 的独立安全缓存事务，增加 authorities.json 和 assessments/<subject>.json。保留报告原文、完整预览、取消/关闭草稿、回退/撤销及中断恢复。历史记录保存每次公钥变更确认及说明。回退作用于当前项目整份参考、评估者和报告缓存；有评估者变化必须重新确认。缓存损坏时可明确选择历史回退修复，未知内容不会被静默接管。

确认时核对当前源、缓存、完整产物、权限、有效期与候选树；先前的完整树重建、防夹带和本机 journal 锚点继续适用。恢复仍不依赖解析损坏 portable MCP 锁。本机 journal 只提供本地完整性检查，不等同于外部存证。

安装审查增加来源证据和安全证据摘要，包含当前完整本机缓存及逐项结果。即使结论仍为 PASS，评估方法、证据地址、评估者信息或来源登记变化也会使旧安装确认失效、旧登记显示 STALE。升级后旧审查若缺少这些摘要，也需要重新核对。历史 SBOM 原样保留。

L2 安装及恢复不要求低信任放行；L0/L1 继续要求本次放行与理由，追加审计记录实际等级。安装界面按当前等级展示并折叠不需要的风险表单，历史恢复低等级登记时仍可填写。安全检查成功不启动程序，安装失败不改产物。

## CLI、API 与界面

复用 `mcp security state/check/plan/apply/recover` 和 `/api/mcp/security/{state,plan,apply,recover}`。原 v0.40 请求兼容；state 增加 authorities、assessment_subjects、assessment_documents、每项 assessment，以及历史 request。独立评估使用以下请求：

```json
{
  "action": "authorities",
  "expected_sha256": "<state.sha256>",
  "authorities": {"schema_version": 1, "authorities": []},
  "authority_confirmation": true,
  "authority_reason": "通过独立渠道核对评估者公钥及来源范围"
}
```

公钥项含来源签名公钥相同的 id/label/public_key/source_type/source_id/expires_at/enabled 字段，另有 provider 和 verification_url。两份登记的用途独立。

assessment-import 使用 document 保存完整 `schema_version/key_id/payload/signature` 报告；assessment-remove 使用 subject_sha256。payload 使用 aidock-plugin-assessment-v1，含 reference、coverage、malware、static_analysis、publisher_identity；coverage 固定为 complete-artifact-runtime-and-resolved-dependencies-v1。输入模板只提供绑定及 UNKNOWN，不可直接作为已签名报告导入。未知字段、缺项、无效公钥/签名、超限及带凭据/查询/片段的地址均拒绝。

check 在全部当前安全证据通过时返回 0，证据未齐返回 3，已记录风险或缓存故障返回 5。此退出码只针对安全检查，不代表 MCP 运行就绪；无项结果也不是通过评估。

管理入口仍为 MCP 定义 → 安装锁与本地产物 → 漏洞参考与安全检查，其中新增「独立评估与信任等级」。参考和评估共用明确的 MCP 选择，公钥/报告/原参考草稿彼此独立；保存一项不会清空其它草稿。提供模板、公钥清单、确认与说明、签名报告原文、前后预览、移除、历史回退及恢复。所有内容以文本显示。

## 验收

功能回归覆盖签名篡改、签名前缀替换、未知/停用/过期评估者、来源/署名范围、权限/产物绑定、部分参考、未来/过期报告、逐项未知或失败、L1/L2 累积、独立风险保留、标准安装与降级放行、旧确认失效、回退确认及损坏缓存历史修复。报告方法及来源登记变化但结论不变，也有过期确认回归。CLI 贯通签名 L2、安全 check 返回 0、标准安装/SBOM 与停用阻断；API 覆盖确认、预览无登记、历史及回退。

内置浏览器使用 .artifacts/experience-mcp-assessment-v041-ui 独立项目完成：未登记评估者拒绝、取消/关闭保留两份草稿、公钥登记、签名 L2 预览确认、标准安装、静态分析 UNKNOWN 降至 L1、模拟恶意代码风险阻断、回退/撤销、停用与需重新确认的恢复、过期降级、重新导入有效报告及最终构建重启。11 条安全历史和 2 条安装审计保留；13 个源文件与 16 个产物文件摘要不变。

390×844 下页面 390/390、弹窗 331/331，三份评估输入框均 290/290，无横向溢出；脚本 warn/error 为零。全部评估与风险数据明确标注为模拟验收夹具，不是对真实插件的扫描报告。五个真实工作目录未访问。测试与构建的最终计数见 .artifacts/experience-mcp-assessment-v041/verification.json。

证据目录还包括 acceptance.cjs、acceptance.json、frontend.json、go-race-final-all.jsonl、go-race-final-mcp-v2.jsonl、l2-assessment-final-desktop.png、risk-assessment-desktop.png、assessment-narrow.png、installed-l2-final.png。最终全仓进程成功退出：802 PASS / 7 SKIP / 0 FAIL、18 包；四包 MCP 聚焦竞态 71 PASS / 0 FAIL。20 组前端、58 项 Demo、21 个 JS 语法、vet、Windows build 与 Linux cross-build 通过，Linux 未运行。最终前端证据另存 frontend-final.json。

## 后续

下一项推进 Profile MCP 组合和按配置层解释启停，再落实安装获取、项目授权、隔离、受管 stdio/出口 broker、凭据及原生接入；真实评估服务适配、在线刷新与 L3/L4 继续保留为未完成。阶段 1E 联合验收和阶段 2–5 不因本增量而提前完成。

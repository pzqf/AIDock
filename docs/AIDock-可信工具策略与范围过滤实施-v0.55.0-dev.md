# 可信工具策略与范围过滤

版本：v0.55.0-dev。项目 MCP 可保存并审查可信工具描述、独立读写分类及固定项目/Public 返回合同；授权时须单独同意工具元数据展示，恢复授权需重新确认。CLI/API/界面共用严格原文解析、输入绑定和源时间线回退。只开放 scoped-public-query-v1 合同，不适配任意第三方 MCP；实际项目插件启动继续开发，阶段 1 与完整 Goal 未完成。

## 配置合同

mcp.yaml 的每个 server 可选 tool_policy；adapter 固定为 scoped-public-query-v1，tools 最多 64 项、策略最多 256 KiB。每项独立声明 access=read|write，descriptor 只接受 name、title、description、inputSchema、annotations。名称不含斜杠、最多 64 字节，拒绝大小写冲突。拒绝重复键、大小写别名、null、未知字段、隐藏 _meta 和任意 schema。

输入 schema 固定四个必填 string：project、scope、copy、query，additionalProperties=false。实际请求逐项匹配当前项目、作用域、副本，不允许额外字段或嵌套替代。返回仅接受相同身份、level=Public、content 以及可选 bool isError；content 最多 64 项、只允许 text，总返回最多 256 KiB。资源链接、structuredContent、额外元数据、其它数据级别和跨绑定返回均拒绝。

这是显式信任的自有插件必须遵守的协议合同，不能从文字语义识别或拦截所有敏感信息，不是 DLP 或 OS 隔离。不应据此接入任意第三方 MCP。annotations.readOnlyHint 不授予权限；既有中继只列出完整 descriptor 摘要匹配的已审查工具，未知工具过滤；write 因没有逐次批准渠道仍拒绝。

## 授权与兼容

工具策略完整绑定到安装锁、有效配置及项目副本授权。策略变化使旧安装锁、登记和授权失效；重新审查后才可授权。新增 confirm_tools 和 tools_confirmed：普通权限、密钥或自有 Tier 0 信任确认不能替代工具确认。缺少独立确认时拒绝授权，旧记录不自动授予工具权限。撤销无需确认；恢复被撤销的工具授权必须再次填写用途和独立确认，签发新授权 ID。

旧配置省略 tool_policy 仍可读，不自动获得工具资格；旧记录省略新字段保持原 JSON 语义。新增字段不被旧二进制认识，含策略的配置不支持直接交给旧二进制；源回退应使用支持该版本的程序。

## CLI、API 与界面

新增 aidock mcp policy-template 和 GET /api/mcp/policy/template，模板仅是草稿，不是实际观察或批准的工具。保存沿用 mcp plan/apply 和源时间线，保留完整原字节回退、撤销及中断恢复。

编辑请求可用 tool_policy_documents 映射规范十进制 server 数组索引到原始 JSON 字符串，最多 64 草稿；空字符串移除策略。后端先严格解析原文，避免浏览器 JSON.parse 吞掉重复键。异常索引、重复键或非字符串拒绝，不回显原值。

MCP 定义可展开编辑 JSON、插入合同示例并预览完整前后变化；无效原文在重绘、重开和取消后保留。权限页列出名称、独立读写分类和完整 descriptor，新增工具单独确认。取消保留用途和确认；切换审查对象或输入变化使确认失效，成功保存清空用途和所有确认。

## 验收

证据：.artifacts/experience-tool-policy-v055/verification.json。七个受影响包完整 race：406 PASS / 1 SKIP / 0 FAIL，退出 0；子进程 helper 跳过不计通过。25 组前端回归、25 JS 语法、go vet、gofmt、diff check、Windows 构建与 Linux 交叉构建通过；Linux 未运行。最初交叉构建调用因遗漏父环境而缺 GOCACHE，修正构建环境后成功；不是产品运行失败。

独立 CLI/API 共 20 项通过：独立工具同意、恢复重确认、不可变输入、模板一致、原文重复键及异常值拒绝、固定 Node 核验和审计、撤销后历史化等。浏览器完成无效原文保留、模板修订、完整对照、预览取消、保存、源回退/撤销及恢复基线，以及缺工具确认阻断、完整描述审查、取消草稿保留、授权成功清空。弹窗 869/869 无横向溢出，浏览器 warn/error 为 0。

最终完整源与产物树分别 12 / 7 条，与 baseline 一致；插件入口标记不存在。未访问或修改 E:/work、work2–5。窄屏与静态 Demo 本轮未重跑。以上不证明实际项目 L3/L4 或插件运行。

## 继续实施

下一增量接通锁定 stdio 启动、会话握手、逐次复核及持久会话审计。Tier 1/2 OS 隔离、原生 MCP 字段联合写入与恢复、外部生成器执行、正式副本发布和阶段 2–5 仍未完成；Goal 保持 active。

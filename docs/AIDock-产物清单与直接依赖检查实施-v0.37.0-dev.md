# AIDock 产物清单与直接依赖检查实施

版本：v0.37.0-dev，2026-10-15。软件实现版本独立于冻结设计 v0.24；本增量不代表阶段 1 或整体 Goal 完成。

## 用户流程

在「MCP 定义 → 安装锁与本地产物 → 产物清单与依赖」查看已保存安装锁的当前产物。支持筛选、重新检查、插件清单原文、权限前后对照、直接依赖表和摘要/检查代码。

明确分别显示清单完整、缺少清单、权限差异、依赖漏报、产物变化等结果。读取不修改源或产物，不影响 MCP 定义或安装锁草稿。关闭再打开会重新读取，并回到弹窗顶部；读取失败清除旧结论。

当前对象仍是 Local Root 中预先备好的产物。尚未登记为已安装，不生成正式 SBOM，不提升信任等级，也不授予授权、隔离或运行资格。恶意代码/CVE 快照、静态分析与发布者身份等独立证据继续开发。

## 清单合同

只读取产物根目录的 `plugin.yaml`，不使用任意目录中同名文件代替。原文最多 1 MiB，沿既有 rooted 完整产物观察读取，拒绝链接、路径别名和超限文档；不会为解析清单第二次打开文件。文档摘要与大小逐一绑定同次观察和安装锁；所有元数据都来自匹配的产物。完整产物未匹配时不把清单解释为有效证据。

沿用 `schemas/plugin.schema.json`，重复字段、未知字段和非法类型拒绝。核对 `type=mcp-server`、固定版本与完整来源，非本地来源的版本也须匹配。manifest 名称保留原声明，允许项目 MCP 使用别名，不把名称当发布者身份核实。

manifest 权限必须与当前保存定义一致；文件路径和网络域名按集合比较顺序，其余值精确比较。权限变大或变小均明确显示差异，用户可展开两份声明核对；不会自动扩大、缩小或授予权限。

新增可选 `dependencies`：

```yaml
dependencies:
  - ecosystem: npm       # npm | pypi | generic
    name: child
    version: 2.0.0       # 固定版本；与现行锁合同一样采用精确三段版本或 commit/hash
```

最多 1000 项。缺失与显式空数组不同，`null` 无效；旧 manifest 没有此字段仍可解析，但直接依赖状态为 UNKNOWN。拒绝浮动版本、重复依赖和非法名称；PyPI 名称按大小写和 `-_.` 规范化检查冲突。PyPI/generic 目前仅检查声明格式，没有完整解析适配器，不宣称依赖完整。

当完整 npm package-lock v3 先通过原有核验后，比较来源包的直接运行、optional 和 peer 依赖及其解析版本，不把传递依赖或根开发依赖加入清单声明。npm 来源支持包根目录分发和顶层 `node_modules/<来源包>` 包装目录；嵌套依赖按现有 Node 祖先查找规则取解析版本。清单中的 npm name 是该直接依赖的导入槽位；npm alias 的实际包身份仍由原有完整锁核对。

无法定位支持的来源包或可选/peer 依赖未安装时保持 UNKNOWN；与已有完整解析内容矛盾时 FAIL。完整解析锁本身不匹配或平台不符也不会让本检查显示整体通过。直接依赖列表仅为安装前检查输入；冻结设计规定的正式轻量 SBOM 等待本机安装登记完成后生成，不在 core 新增完整传递漏洞分析器。

## 接口与诊断

```powershell
aidock mcp manifest list --json
aidock mcp manifest check --name <已保存锁条目的 MCP 名称> --json
```

命令沿用明确项目绑定参数。`list` 读取结构化结果；`check` 的状态只指本次清单、权限及直接依赖范围，UNKNOWN 退出 3、FAIL 退出 5。即使清单检查 PASS，也不能作为整个插件安全门通过。未知名称返回 `MCP_LOCK_ENTRY_MISSING`。

API：`GET /api/mcp/manifest/state`，可选 `name` 参数。沿用 session/Origin、逐请求 `X-AIDock-Binding` 和严格方法控制，不提供写接口。结构化状态明确包含 `installation_state=PREPARED`、`sbom_status=UNKNOWN`、`execution_ready=false`。

安装锁的 `manifests` 给出每个条目的完整报告；同一检查发现进入 MCP 定义、doctor 和原生工具预览的既有诊断链。核验摘要继续绑定当前产物与发现，不能复用改变产物之前的确认。

## 验收记录

独立项目 `.artifacts/experience-mcp-manifest-v037-ui/`；脚本、结果与截图 `.artifacts/experience-mcp-manifest-v037/`。未访问五个真实工作目录。

已覆盖包根目录/包装目录、直接/传递依赖、权限扩大与顺序变化、漏报/重复/浮动声明、缺少清单、缺少可选解析、重复 YAML 字段、原字节篡改及 typed 输入锁合同。源集成验证 BOM/CRLF/注释保留、重复读取、当前产物变化、L0/未安装/不运行状态；API 验证认证、方法与项目绑定，CLI 验证三种退出状态。

内置浏览器验证三类结果、权限声明前后对照、筛选、篡改后旧通过失效、恢复原字节后重检、定义和锁草稿保留。390×844 页面宽/滚动宽 390/390，弹窗 331/331，重开 scrollTop=0，页面 warn/error 为 0。最终核对 13 个源文件和 16 个产物文件摘要不变，无原生工具目录、安装登记或正式 SBOM 创建。截图 `saved-manifest-desktop.png`、`saved-manifest-narrow.png`。

Windows 全仓竞态回归 746 PASS / 7 SKIP / 0 FAIL，17 包；最后补充空安装锁不能让已定义 MCP 通过的修正，来源包追加竞态 2 PASS / 0 FAIL。17 组前端、58 项 Demo、JS 语法、gofmt、vet、Windows 构建、Linux 交叉构建与 101 个文档相对链接检查通过；Linux 未运行。内置浏览器独立项目验收通过，13 个源文件和 16 个产物文件摘要不变，未登记安装或生成正式 SBOM。

验证汇总在 `verification.json`；后续继续安装登记、正式 SBOM、安全评估及完整计划其余工作。

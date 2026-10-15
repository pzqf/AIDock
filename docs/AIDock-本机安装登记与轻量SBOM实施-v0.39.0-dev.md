# AIDock 本机安装登记与轻量 SBOM 实施

版本：v0.39.0-dev，2026-10-16。软件版本独立于冻结设计 v0.24；阶段 1 与整体 Goal 继续进行。

## 用户流程与范围

在「MCP 定义 → 安装锁与本地产物 → 本机安装与 SBOM」核对当前产物、清单、来源及文件/网络/子进程权限，填写放行理由后预览登记，再确认。当前只登记 Local Root 中已备好的产物，不复制大型运行时、不下载、不启动第三方程序、不授予运行权限。

完整产物匹配、有效 plugin.yaml、清单权限与定义一致、明确直接依赖声明为必要条件，任何已知失败均阻断；目前只开放 stdio 定义。缺安全评估保持 L0，默认安装门槛为 L2。每次登记及恢复一个登记均需 allow_unverified=true 与非空理由，不能将来源签名、文件匹配或用户放行解释为 L2。

预览不会落安装登记或生成正式 SBOM。确认后，才为本机已登记插件生成 aidock-direct-sbom-v1：保留插件/发布者名称、版本、来源、产物和清单摘要、登记身份及时间，只列 plugin.yaml 直接声明的依赖。该清单不是完整传递依赖或漏洞扫描报告。产物、清单、来源或当前检查变化时登记显示 STALE，SBOM 明确标注为登记时的历史清单；execution_ready 继续为 false。

## 持久化、审计与恢复

本机登记绑定项目、作用域、物理源目录及 Local Root，不随共享源或副本同步传播。plugin-install-<绑定摘要> 保存 binding.json、installed 与 audit；独立 plugin-install-transactions 保留计划、备份和 journals。公开修改由源锁串行化；来源读取结束后再取安装锁，恢复不依赖解析 portable MCP 锁。

确认同时核对整个源摘要、完整当前检查、原登记状态及明确请求，并重新构建完整候选树。修改候选后重算摘要不能夹带其它文件。登记、移除、回退与撤销均追加审计，hash 链还与已提交事务核对；可检测截断、修改和审计外状态漂移。两处本机存储同时被有权限的主体重写不属于外部可信存证保证。

移除或回退仅改变登记，保留产物和追加审计。恢复旧登记前重新核对当前依赖、权限和安全证据，不一致返回 SNAPSHOT_DEPENDENCY_MISSING；需重新明确放行并记录本次理由。进程中断使用 mcp install recover 完成既有事务恢复，损坏 portable MCP 锁不会妨碍该恢复或被覆盖。读失败会清除旧展示，关闭弹窗/取消预览保留标签页草稿，成功保存清空本次放行。

## CLI 与 API

沿用明确 managed-root/home/project/scope/copy/native-root 绑定：

```text
aidock mcp install state
aidock mcp install plan --file <请求.json>
aidock mcp install apply --plan <UUID> --expected-digest <摘要>
aidock mcp install sbom --name <MCP名称>
aidock mcp install recover
```

登记请求：

```json
{
  "action": "install",
  "expected_sha256": "<install state 返回的登记摘要>",
  "name": "checked-fixture",
  "allow_unverified": true,
  "risk_reason": "核对当前产物和权限后，仅登记本机安装"
}
```

remove 接受 name 且不接受放行字段；rollback 使用 snapshot，恢复登记时带本次风险确认。API 为 GET /api/mcp/install/state、GET /api/mcp/install/sbom?name=...、POST /api/mcp/install/plan、/apply、/recover。写接口沿用会话、CSRF、方法、严格请求及绑定校验，apply 接受 plan_id/digest。

## 验收证据

Windows 全仓竞态：764 PASS / 7 SKIP / 0 FAIL，17 包，命令为 go test -race -count=1 -p 2 -timeout 20m -json ./...。全仓启动后的最终 MCP 门槛和历史字段修改，另跑 source/cli/console 三包 TestMCP 竞态：41 PASS / 0 FAIL。没有把包级通过数计作测试用例。19 组前端状态回归、58 项 Demo、20 个 JS 语法、gofmt、vet、Windows 构建及 Linux 交叉构建通过；Linux 未运行。

新增测试覆盖显式风险/理由、缺清单与权限矛盾阻断、非 stdio 阻断、预览无登记/SBOM、重复确认、摘要过期、另一 home 隔离、确定性 SBOM、重启、移除/回退/撤销、依赖恢复阻断、审计篡改/截断、重算摘要夹带文件。真实子进程 exit 94 后由新 Manager 恢复，portable 锁损坏保留。

内置浏览器使用 .artifacts/experience-mcp-install-v039-ui 独立项目，完成预览/取消/确认、child@2.0.0 直接依赖、产物变化 STALE、恢复原字节 CURRENT、回退/撤销、移除/恢复和重启。SBOM 未混入传递依赖 leaf，5 条审计与各次理由保留。关闭重开保留理由草稿；390×844 页面 390/390、弹窗 331/331、表格 258/258，无横向溢出，脚本 warn/error 为零。

13 个源文件和 16 个产物文件摘要不变，没有创建原生工具目录。证据在 .artifacts/experience-mcp-install-v039：acceptance.cjs、acceptance.json、verification.json、go-race.jsonl、go-race-final-mcp.jsonl、installed-sbom-desktop.png、installed-sbom-narrow.png、stale-installation.png。五个真实工作目录未访问。

## 后续

继续本地 CVE 快照与独立外部评估缓存、安装获取、Profile MCP 组合、项目授权、OS 隔离、受管 stdio 与出口 broker、原生接入及完整计划。安装登记不是受管运行完成证据。

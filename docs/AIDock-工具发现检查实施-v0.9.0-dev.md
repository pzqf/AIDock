# AIDock 工具发现检查实施 v0.9.0-dev

日期：2026-10-14。需求基线仍为 v0.24。此增量属于阶段 1C 的诊断子集，未开放原生目标写入、工具回退或跨副本发布。

## 已交付

管理界面「AI 工具」增加两个步骤：先检查接入，再预览应用。文件检查默认不启动进程；「验证工具发现」需要在界面确认后才运行所选工具。结果逐工具、逐技能展示，并分开列出项目文件一致、隔离项目发现、实际会话加载和能力验证。

CLI 与界面共用 `internal/native.Manager.Check` 和 `Latest`：

```powershell
# 以下命令均须携带同一个专用测试项目的显式绑定参数：
# --managed-root <project/.aidock> --home <隔离数据根>
# --native-root <project> --project <UUID> --scope <scope>
aidock tool check --all --json           # 静态文件状态，不启动工具
aidock tool check --all --probe --json   # 明确启动隔离发现检查
aidock tool check --tool codex-cli --probe --json
aidock tool check --last --json          # 读取最近检查并重新核对过期状态
```

新增 `tool check` 是分层诊断的实施命令入口，不改变设计的源保存/工具应用/副本发布边界。`--last` 与工具选择、`--probe` 互斥；工具选择拒绝未知工具及重复项。界面/API 不接受调用方提供的 executable、cwd 或目标路径。POST 保留认证、Origin/Host/CSRF 及严格 JSON 校验。

检查有完整报告时，CLI 仍返回退出码 **3 / EVIDENCE_INCOMPLETE**，因为实际会话加载/能力没有证据；发现或静态失败返回 **5 / STATIC_CHECK_FAILED**。`--last` 发现过期返回 **3 / CHECK_STALE**。HTTP 请求成功表示检查已完成，报告中的 PASS/FAIL/UNKNOWN 才是各项结论。`doctor` 只读取最近检查并核对其输入，不自动启动探测进程。

## 发现方式与证据范围

| 工具 | 此次实际验证版本 | 本地接口 | 已证明 | 未证明 |
| --- | --- | --- | --- | --- |
| Claude Code | 2.1.257 | stream-json 的 initialize 控制请求，读取 commands | 隔离项目启动时的技能命令名称/描述 | 项目用户配置、父目录/个人同名覆盖、模型已读正文或已使用技能 |
| Codex CLI | 0.162.1 | app-server stdio initialize → initialized → skills/list，forceReload=true | 指定隔离 cwd 的确切 SKILL.md 路径、名称/描述、enabled=true | 用户配置下禁用状态、实际项目会话已加载正文或能力有效 |

`discovery_probe_versions` 只允许已实际观察的精确发现协议版本；不匹配则 UNKNOWN，不尝试其初始化协议。`verified_versions` 仍为空，原生写入保持关闭。一次隔离发现 PASS 不会被提升为真实项目 L2 或 L3/L4 证据。

探测只复制已保存、已启用完整包的缓存字节，保留附件、空目录和原生 frontmatter；页面草稿不参与。测试项目有独立仓库边界，阻止扫描 AIDock 仓库的父级技能。子进程使用空工具 home、最小环境，不继承个人凭据或工具配置。Claude 关闭 hooks，MCP 使用 strict 配置且没有配置服务器，不发送 user/model 提示；Codex 不启动 thread/turn。两者均不调用生成器或技能脚本。

版本探测最多 5 秒，发现初始化最多 20 秒；输出和单条协议消息有大小上限。结束/错误/取消时关闭输入、终止并等待直接子进程，再清理本次临时项目。正式 OS 隔离、完整后代进程约束和网络出口治理仍待实现，不能将空配置当作 OS 沙箱。插件、MCP 或技能脚本执行未开放。

Windows npm 的 codex shell shim 解析为已安装包中的原生 `codex.exe`，不拼接 shell 命令。修复了 Windows CreateProcess 不接受过长 cwd 的问题：临时项目位于较短的 `Local Root/native-probes/`，独立于持久检查的身份哈希目录。实际 CLI 重复启动三次通过。

`user-invocable: false` 的 Claude 技能缺少命令列表证据时返回 UNKNOWN；不会直接解释为失效。名称/描述不同、错误路径、禁用结果、重复命中或解析错误不能成为发现 PASS。

## 持久检查与过期

报告位于显式 Local Root 的 `native-checks/`，按项目根、owner、scope、copy 隔离。记录 schema v1 包含精确版本、工具程序 SHA-256、完整源/目标/适配器输入摘要、包 id/revision、结果和检查时间。它是观察记录，不是源事务或工具恢复点。

最近记录在重启后可读取。源、目标、外部绑定、适配器、工具安装路径/程序字节变化，或记录超过 24 小时，都使记录过期。过期结果保留原始观察，但界面全部标为历史，不能当作当前生效证据。无法重新核对输入时也标过期。记录身份/摘要损坏则明确拒绝读取。

检查结束以及写入记录前都会再次核对输入；检查期间修改源/目标/工具时返回 PLAN_STALE。未完成源事务继续阻断新检查。报告不保存原始 CLI 日志、用户技能列表或账户/凭据数据；协议错误仅保留受限的诊断原因。

技能源 Schema、固定包修订、草稿和既有源回退/撤销机制没有变更；v0.8 的 native-config 计划遇到新增适配器证据字段会按原有摘要机制失效，需重新预览。

## 本机验收

- Go 全仓 race、vet、构建和四组 Node 状态回归通过；原 Demo 58 项回归通过。远端 CI 未运行。
- 单元/负向检查覆盖：静态检查不启动工具、完整包保留、错误路径/禁用/缺失/重名/解析失败、未安装/未知版本/协议失败、阻断目标、检查中源变化、工具程序/目标/时间过期及损坏记录。
- 本机可选集成：`$env:AIDOCK_NATIVE_PROBE_TEST='1'; go test -v ./internal/native -run TestLocalDiscoveryIntegration`。仅在测试临时目录使用当前 CLI；完整包附带二进制附件、空目录与 allowed-tools。Windows 2.1.257 / 0.162.1 的隔离发现通过，L1 NOT_APPLIED、L3/L4 UNKNOWN。
- 内置浏览器：确认/取消、静态检查、两工具真实发现、检查期间草稿保留、CLI 报告在重启后被 UI 读取、人工目标修改使记录过期、480px 无横向溢出；console error/warn 为空。
- 浏览器专用实例为 `.artifacts/experience-discovery-20261014/`。验收前后 **11 个源文件 SHA-256 一致**。检查未写入工具目标；用于过期注入的人工 `.agents/skills/user-note/SKILL.md` 已清理。CLI 报告在 `.artifacts/discovery-cli-result.json`，截图在 `.artifacts/aidock-tool-discovery-v0.9.png`。
- `E:/work`、`E:/work2–5` 本轮没有读取、修改或作为测试目录。用户原体验页面未刷新或停止；新增页面使用独立项目。

## 下一步

阶段 1C 下一步仍是原生多目标持久事务与恢复、工具回退/撤销，以及实际项目发现/新会话正文加载证据。必须通过第二目标失败、进程中断、恢复冲突、手写内容保留和预览过期验证，再开放工具写入。随后进入 1D 的专用测试副本同步；阶段 1 总验收仍未完成。

## 官方依据

本次使用 OpenAI Docs 核对并实际获取以下官方页面：

- [Codex skills](https://developers.openai.com/codex/skills/)
- [Codex app-server / skills/list](https://developers.openai.com/codex/app-server/)
- [Claude Code skills](https://code.claude.com/docs/en/skills)
- [Claude Agent SDK TypeScript / initialize、supportedCommands](https://platform.claude.com/docs/en/agent-sdk/typescript)

官方页面用于路径与协议契约；精确版本兼容只依据上述本机隔离探测，不由文档版本号推断。

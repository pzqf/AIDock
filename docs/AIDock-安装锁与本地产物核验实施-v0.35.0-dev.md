# 安装锁与本地产物核验 · v0.35.0-dev

日期：2026-10-15。冻结设计 §9.1、§19.4 的阶段 1C 增量。阶段 1 与整体 Goal 继续进行，所有后续必做仍在开发计划内。

## 用户流程

打开项目的「MCP 定义 → 安装锁与本地产物」。界面显示本机 Local Root 的 `mcp-artifacts` 根目录。先在其子目录备好产物，选已保存的 MCP，填写子目录、运行时类型、精确版本和产物内相对路径，再生成候选清单。远程来源的产物版本沿用 MCP 固定版本；本地来源需明确填写产物版本。只读该指定目录，不访问 MCP 的项目来源路径，也不下载或执行程序。

候选包含完整文件、隐藏文件、空目录、入口与运行时的哈希和权限模式。支持 npm package-lock v3：导出全部解析包的路径、名称、固定版本、来源和完整性声明；核对完整解析锁、本地每个 package.json 及依赖图。其它解析格式仍显示「仅声明」，不冒充完整依赖已验证。

生成候选后的检查与已保存锁的检查分别显示。可以导入 JSON/YAML 原清单或核对全文；「预览保存安装锁」显示前后清单和候选检查，确认后才更新当前项目 `.aidock/plugins.lock`。长对照显示前 48K 字符，候选全文保留在编辑框。取消、关闭和保存失败保留草稿；有草稿时不允许另一轮目录生成覆盖它，重读需再次确认放弃。保存或重读锁同时刷新 MCP 定义页的已保存诊断，保留尚未保存的定义草稿并清除旧确认。

锁文件、来源、权限或本地产物在预览后变化，会使旧确认过期。时间线支持恢复到锁保存前、撤销回退和恢复中断保存。恢复保留 BOM、CRLF、注释乃至原先无效锁文件的完整字节。回退范围为当前项目整个源；本地产物始终只读核验，不随锁回退改写。

## 锁合同与证据边界

新增 `schemas/plugins-lock.schema.json` v1，顶层为 `schema_version` 和 `plugins`。每项包含名称、MCP 定义摘要、完整来源、精确版本、平台、入口、运行时精确版本/等号约束/可执行文件、产物子目录与完整清单摘要、依赖解析格式/文件摘要及完整依赖表。

定义摘要绑定名称、来源、传输、入口、权限和 Tier，排除仅表达包含意愿的 enabled。改变权限必须重新评估，不能复用旧锁授权。拒绝未知字段、自填信任等级、大小写重复、目录重叠、路径逃逸、设备名、缺父目录、入口或运行时不在清单、浮动版本、来源版本不符及完整清单摘要不符。

产物相对路径固定属于 Local Root 的 `mcp-artifacts/`，锁内不保存机器绝对路径。完整树核验拒绝符号链接/reparse point、非普通文件、额外文件和目录；逐文件检查读取前后身份、大小、修改时间和权限模式。通过根目录句柄读取，运行时流式计算摘要，不受源配置单文件 10 MB 限制。产物单文件上限 256 MB、合计 512 MB、合计最多 20000 项；依赖文档单项 8 MB、合计 32 MB，源配置另有 10 MB/节点上限。每轮锁核验有 30 秒上下文上限。

npm v3 检查只接受自包含的完整 packages 清单，拒绝 link/workspace。依赖声明须与解析锁及本地元数据一致，必需依赖和非可选 peer 可解析，清单以外的本地依赖被拒；缺少文件、来源或完整性声明失败。固定版本须满足依赖、peer、可选及根开发依赖的 semver 范围，npm 别名同时核对实际包身份；未支持的版本约束明确失败。这里复用固定版本的轻量 semver 库验证外部解析结果，不运行解析器安装依赖。解析包来源使用不带凭据/查询参数的 HTTPS URL，摘要声明接受 SHA-256/SHA-512。这里核对的是解析声明与本地产物一致，**没有重新下载归档验证上游 SRI，也没有执行 npm install、构建脚本或运行时**。

`artifact_status`、`dependency_status`、来源 `trust_level` 与 `execution_ready` 分别输出。本地自算哈希只建立完整性基线，来源始终 L0，运行时版本仍为声明；没有可信发行证明时不声称已验证版本，不调用 --version。文件匹配不能升级为 L2、项目授权或 OS 隔离通过，所有执行资格仍为 false。文件模式核对不证明 Windows ACL、恶意同用户并发防护或跨平台运行。

有效锁的实际诊断接入 MCP 定义、已启用项 doctor、Profile 与原生工具预览。坏锁明确为 `MCP_LOCK_INVALID`；平台、定义与完整文件的失配有各自原因，不再仅凭文件存在返回统一未知状态。

## CLI 与 API

```powershell
aidock mcp lock list <项目绑定参数> --json
aidock mcp lock check <项目绑定参数> --json
aidock mcp lock inspect --file prepared.json <项目绑定参数> --json
aidock mcp lock plan --file edit.json <项目绑定参数> --json
aidock mcp lock apply --plan <UUID> --expected-digest <摘要> <项目绑定参数> --json
```

inspect 的输入为 `{name, artifact_path, version, runtime}`；runtime 为 `{kind, version, constraint, executable}`，constraint 使用 `=精确版本`。输出候选完整 document 和读到的 expected_sha256。plan 的输入为 `{expected_sha256, document}`，apply 只确认已持久计划和摘要；二次核验本地结果及 Local Root 绑定，只允许修改 plugins.lock。list 读取成功为 0；check 的失败为 STATIC_CHECK_FAILED/5，待验证为 EVIDENCE_INCOMPLETE/3。空锁通过只表示没有锁定项可查，MCP 定义完整性仍由 mcp check 检查。

API：GET `/api/mcp/lock/state`；POST `/api/mcp/lock/inspect`、`/api/mcp/lock/plan`、`/api/mcp/lock/apply`。复用项目请求绑定、会话鉴权、Origin/CSRF、严格解码和恢复门槛，不接受任意 shell 命令。专用确认拒绝其它源动作，受管执行入口仍未开放。

## 验收证据

证据目录 `.artifacts/experience-mcp-lock-v035/`，独立界面项目 `.artifacts/experience-mcp-lock-v035-ui/`。固定虚构包和非可执行运行时占位文件仅用于读字节核验，从未安装或启动。

测试覆盖清单歧义/路径/版本拒绝、隐藏文件/空目录/权限/运行时/额外文件变化、完整 npm 依赖和未闭合图、原字节保存/重启/回退/撤销、中断恢复、源/产物/本机绑定过期、CLI 退出码、API 安全和专用确认、前端草稿与候选/保存检查分离。内置浏览器已执行生成、取消不写入、产物变化阻断旧确认、保存、回退/撤销、越界拒绝、自填信任拒绝与权限变化失配。390×844 页面宽度 390/390、弹窗 331/331，重开 scrollTop 为 0，浏览器 warn/error 为 0。

最终 Windows 全仓 race 正常退出 0：**670 PASS / 7 SKIP / 0 FAIL，15 个包通过**。7 项跳过包含本机未提供的链接权限等平台条件；不将跳过算作通过。此前完整回归为 657 PASS / 7 SKIP / 0 FAIL，保留在 `go-race-before-range-check.jsonl`；补充原生门槛测试的五包聚焦为 48 PASS / 1 SKIP / 0 FAIL，保留在 `focused-before-range-check.jsonl`，两者不替代最终完整回归。15 组前端状态、58 项 Demo、全部 JS 语法、gofmt、vet、Windows 构建及 Linux 交叉构建通过；Linux 未运行。

最终浏览器另验证了版本范围矛盾：文件清单重新生成后仍报告 `2.0.0 不满足 ^3.0.0`；恢复测试字节后重新核验匹配。服务正常退出并重启后保留原锁和源时间线，诊断刷新保留 MCP 定义草稿。原项目 11 个源文件摘要不变，只有新增 mcp.yaml 和 plugins.lock；6 个测试产物文件的最终摘要全部不变，撤销回退后锁文件与生成候选字节完全一致，未创建原生工具目录。最终汇总和字节核对脚本为 `acceptance.json` / `acceptance.cjs`。

## 继续开发

此增量完成锁合同、生成候选、本地完整性核验与恢复管理；尚无可信发行/安全评估证据与运行时发行验证、安装获取事务、安装安全门放行、项目授权/凭据审计、Profile MCP 组合、受管中继启动和原生配置。继续按计划落实这些能力，再收口实际会话、外部生成器治理、正式副本发布及阶段 1E，随后推进阶段 2–5。五个真实 work 目录未访问、未修改、未用作测试。

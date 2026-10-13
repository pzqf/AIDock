# AIDock 全局技能库与固定继承实施

日期：2026-10-13。实施版本：**v0.4.0-dev**。需求基线：v0.24。阶段 1B 的新增子集交付，阶段 1B 整体尚未验收。

全局技能库可以创建/导入、编辑、展示信息维护、改名、启停、退役/恢复和源回退。项目选择明确的全局 UUID/精确修订后保存固定继承关系及完整不可变包；发布全局新候选不会自动更新项目。项目可以从固定版本生成同 ID 整包覆盖、新 ID 独立分叉，或恢复继承并保留自定义历史版本。CLI 与管理页面使用同一 source engine 和持久事务。

## 源与身份契约

| 对象 | 当前实现 |
| --- | --- |
| 全局管理 | 显式 `--global-root` 和 `--home`；owner/scope 均为 global，身份在 `library.yaml`。非空无身份目录禁止自动接管，与项目管理绑定互斥 |
| 项目管理 | 显式 `--managed-root`、项目 UUID、scope、home；附加 `--library-root` 只用于读取已初始化全局库，HTTP 请求不能改写绑定根 |
| 项目 owned 包 | 项目 `skills.yaml` 与 `skills/<UUID>/`；全局库采用相同包索引与事务契约 |
| 继承关系 | `skill-inheritance.yaml` 保存项目 owner/scope，以及全局 owner/scope/Skill 完整固定记录；保留完整包 `.revisions/<UUID>/<revision>/` 和治理记录 `.revision-records/<UUID>/<revision>.json` |
| 固定继承 | 初次采纳为本层停用，后续采纳保持既有开关；Profile pin 与所选包修订相符。纯继承只读，允许查看、本层启停、引用、覆盖和分叉 |
| 项目覆盖 | 沿用全局 UUID，明确 `base_source` 与 `base_revision`；完整包覆盖，不合并正文/附件字段 |
| 独立分叉 | 新 UUID 与不同原生名，记录来源 owner/scope/id/revision，清除覆盖基础，初始停用；不改变来源包。原生名变更保留原生 frontmatter 的非名称部分，正文中的动态名称调用仍需人工复核 |
| 恢复继承 | 去除 owned 活包与覆盖记录，Profile 使用已选上层 pin；完整自定义旧修订仍在。保存记录可找回自定义包，也可撤销回退 |
| BASE_STALE | 仅当项目显式更新的上层 pin 与覆盖基础不同才显示；全局新候选不会触发。当前允许保存协调中的源，原生应用功能整体仍关闭 |

项目库观察备份写入项目 Local Root，不写库包或库的事务区；共用库父目录 OS 锁防止活动写入时并发读。Global/Local/项目根拒绝重叠和链接路径。只读库访问会使用父目录锁文件；不声称是完全不创建任何观察文件的文件系统扫描。

缓存不依赖可访问的全局库：库未初始化/暂不可读取时，页面显示原因，项目本地资产与固定缓存仍可使用。缺失/被篡改的固定包或文件清单会阻断该项目源解析，不静默选择最新候选。只有新增采纳计划依赖库可读取。

## 语义修订与兼容

原 v0.2/v0.3 项目修订缺少 `revision_format` 时继续按原完整事务树摘要读取、编辑和回退，避免旧计划和历史版本失效。全局新包、覆盖和分叉使用显式 `revision_format: semantic-v1` 与 `package_digest`；既有语义包编辑继续使用该格式。没有批量隐式迁移旧项目。

`semantic-v1` 规范如下：UTF-8 compact JSON、Go encoding/json 的确定性键排序；包内容清单按路径排序，包含路径、目录标记、原始 SHA-256 和字节数，保留空目录；其 `sha256:` 摘要成为 package_digest。语义对象包含 format、package_digest、native_name、package_format、files（含 text/binary）、dependencies、compatibility、requirements；后三项空值统一为空对象。排除资产 ID、owner、展示名、标签、人工版本、路径绑定、时间和平台原生 mode bits。来源、基础与分叉元数据单独保存，不字段级合并。治理结构受 skills Schema 约束。

平台原生权限仍在完整事务树中用于同平台恢复，Windows 权限映射不等同 Unix 执行位；跨平台 mode 不参与 portable 内容身份，也不构成权限或隔离证明。 golden 固定编码与空值规则，测试显式更改 mode 表示后仍得到相同语义摘要。Linux 当前仅交叉编译，没有 Linux 运行或权限验收。

全局修订记录去除展示名、标签、人工版本后保存完整治理元数据，同一修订禁止改写；目录和原生文档/文件清单/字节摘要必须一致。当前没有依赖、兼容、执行需求的编辑表单或 mutation action；纳入版本身份不代表完整依赖/运行条件已得到验证。

## 预览、过期和恢复

`inherit`、`override`、`restore-inheritance`、`fork` 使用严格动作字段，均要求明确修订。fork 可以指定新 UUID，否则预览时生成 UUID，结果在 diff/后续索引中可审核。新创建/import 的独立资产不能复用已继承 UUID，原生名冲突会阻断。恢复继承按显式已选 pin 生成计划，不把全局当前候选当恢复版本。

采纳计划在原持久 Plan 中增加可选 inputs，绑定库根、UUID、精确修订及规范治理记录摘要；inputs 纳入计划摘要。确认时重新校验该精确旧版本仍存在、包与元数据完整且未退役。发布别的新候选不会使旧版本计划过期；换库、退役、缓存/记录变动则 PLAN_STALE，项目源保持原字节。旧 v0.2/v0.3 无 inputs 的计划序列化保持兼容；inherit 计划缺少来源 inputs 会被拒绝。

全部源变更沿用完整 Before/After 树、blob、事务 journal、磁盘摘要比较、失败自动恢复和真实进程退出后的恢复。rollback 额外保留不可变修订和治理记录。未保存文本草稿会阻止覆盖/恢复/分叉/采纳该技能；失效保存仍保留草稿。该草稿为页面内存状态，浏览器刷新仍会丢失未保存内容。

双控制台验收发现 Cookie 不区分端口；已改为按监听端口区分 Cookie，并测试独立会话与跨控制台拒绝。HttpOnly/SameSite/Origin/CSRF/loopback 校验沿用原实现。

## 验证证据

| 层级 | 结果与边界 |
| --- | --- |
| Go race | **167 PASS、1 SKIP、0 FAIL**（包括子测试），日志 `.artifacts/inheritance-go-race.jsonl`。唯一 SKIP 为 Windows 普通 symlink 权限；junction 越界负向仍通过 |
| 核心新增专项 | 两项目隔离、候选不更新旧 pin、旧版本预览不绑定新候选、BOM/CRLF/二进制/空目录缓存、覆盖/分叉/恢复/回退/撤销、BASE_STALE、同 ID/同名冲突、污染缓存与身份/路径拒绝、来源退役/换库/包/记录修改阻断、失败原字节恢复、真实进程中断恢复、全局引用 tuple、语义 golden |
| CLI/HTTP | 显式全局/只读库绑定、非法组合与缺少 home 拒绝、共享 engine 等价、客户端不能换根、只读继承文件、双端口会话 |
| Node | 只读状态 **5 PASS**、源状态 **28 PASS**、旧 Demo **58 PASS**；DOM/计时器替身不等同浏览器或外部工具运行 |
| 本地构建 | gofmt、go vet、Windows build 与 Linux/amd64 交叉编译通过；没有远端 CI、Linux 运行或部署证据 |
| 内置浏览器 | 新模拟库及项目，**18 项 PASS**。双会话、固定旧 pin、草稿保护、BASE_STALE、找回自定义/撤销、分叉来源、退役过期拒绝、附件只读、键盘、900/390 无横向溢出、库不可用时缓存读取、无 console error、正常关闭 |
| 操作后磁盘 | **8 项 PASS**。其他项目旧 pin/无 owned 包、恢复到所选新 pin、分叉新 ID/来源、自定义旧修订完整、三个包二进制原字节、旧文档 BOM/CRLF/comment、全局历史完整包、restore/rollback 历史保留 |

浏览器证据 `.artifacts/inheritance-browser-evidence.json`，磁盘证据 `.artifacts/inheritance-browser-disk-evidence.json`；截图 `aidock-inheritance-{final,medium,narrow,offline}.jpg`。最终模拟目录 `.artifacts/inheritance-browser-cc5e49a4-1286-4a0a-8ffa-1d55521b3a3d/` 保留；库不可读取模拟已恢复；两服务正常退出、tab 关闭、临时 viewport 重置。初轮用于发现 Cookie 问题的夹具另行保留，不以该轮算最终通过。

## 未完成门槛与下一步

当前仅实现全局库→项目的固定继承。多层 extends、副本本机/临时覆盖、开关完整来源推导、可移植导出、外部权威源桥接、插件技能固定包和治理未完成。依赖环/required/optional、平台/工具精确兼容、运行入口和实际能力检查仍待完成；Schema 和版本一致不能替代这些门槛。

跨项目/离线引用登记、物理删除、tombstone 传播、审计哈希链、原生工具配置应用、生成器集成、跨副本同步、OS 隔离仍未开放。下一步继续 1B 的依赖/兼容预检及原源桥接，在这些门槛完成后进入 1C；不能用本交付声明完整 1B 或真实工具生效已验收。

本轮未扫描/修改 `E:/work`、`E:/work2–5`，没有执行真实技能脚本、生成器、Git/SVN 更新或提交，也没有 commit/push/publish。历史 v0.23/v0.24 归档和 v0.1/v0.2/v0.3 实施证据保持原样；新检查清单与源码快照单独保存。

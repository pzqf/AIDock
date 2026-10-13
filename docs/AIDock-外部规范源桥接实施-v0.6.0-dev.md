# AIDock 外部规范源桥接实施记录 · v0.6.0-dev

日期：2026-10-13。本记录覆盖阶段 1B/1D 的外部 `external-source` 桥接只读子集，不声明工具应用或跨副本发布已完成。

## 已交付

- 新增 `bridge.schema.json` 和独立 `bridge.yaml` 登记模型。每条登记固定 project/scope/copy、外部根、`source_path`、明确的 `source_role: external-source`、`format: native-skill`、生成器标识、可选扫描包清单和批准基线 revision。相对 root 以登记文件为基准解析；省略 packages 扫描整个声明源，指定时只扫描这些包。
- `internal/bridge` 只读检查拒绝重复身份、重叠绑定、路径逃逸、包内链接/junction、缺失外部目录和登记包缺少 `SKILL.md`；同一 project/scope/copy 的非重叠外部源也检查原生重名。包内静态失败传到桥接总结果，敏感文件/缓存等排除项使完整候选不可用。
- 每个完整包的 `external-package-v1` revision 包含全部普通文件原字节、二进制附件、目录项和标准化可执行位；读取后与盘点清单比较，并检查附件 Markdown 引用。`external-v1` 聚合候选固定源路径、格式、生成器声明和排序的包 revision；失败或不完整时不输出聚合 revision。外部内容变化与登记的批准基线不匹配报告 `EXTERNAL_SOURCE_DRIFT`。
- CLI：`aidock skill bridge-check --file <bridge.yaml> --json` 与 `aidock skill bridge check --bridge-file <bridge.yaml> --json`；`doctor --bridge-file` 同时展示 `external_bridges`。
- 独立只读控制台：`aidock config --bridge-file <bridge.yaml>`，无需绑定可写项目。源管理页附加同一参数也可检查；共享面板展示来源、候选、登记基线和完整附件摘要，HTTP API 为只读 `GET /api/bridge`。CLI/doctor 的 UNKNOWN/WARN 和 drift 返回 3，静态失败返回 5；HTTP 请求成功仅表示检查完成，报告的 FAIL/STALE 原样展示。

## 明确边界

桥接登记不等于 AIDock 接管。外部目录继续是唯一权威源；本版本只接受 `allow_edit: false` 或省略该字段，没有编辑、写回或自动采纳路径。来源角色依据用户清单声明，不据此声称已经验证原源/镜像的生成关系。生成器只记录身份，暂存/恢复能力保持 `UNKNOWN`，基线一致也不升级为 PASS。

`approved_revision` 是登记文件提供的比较基线，当前没有批准操作、不可变批准包缓存或离线恢复保证；不得将手填基线当作已执行批准事务。后续显式采纳须经过完整缓存/来源 tuple/计划摘要/回退事务，保持外部源权威。扫描是有界只读观测，不是防止外部生成器并发写入的锁、原子快照或 OS 执行隔离。

## 使用示例

仓库自带合成登记 `examples/fixture.bridge.yaml`；检查不会执行样例代码：

```powershell
go run ./cmd/aidock skill bridge check --file examples/fixture.bridge.yaml --json
go run ./cmd/aidock doctor --bridge-file examples/fixture.bridge.yaml --json
go run ./cmd/aidock config --bridge-file examples/fixture.bridge.yaml
```

默认样例未登记批准基线、生成器能力未验证，前两个命令返回 3 是预期结果。来源 schema 校验可用 `doctor --file <bridge.yaml> --kind bridge`；该命令只校验登记格式，不扫描外部目录。

## 验证

- Go race、vet、构建；桥接测试覆盖限定包范围、摘要/空目录变化、附件引用、排除项、缺包/源消失、过期清单、重名、取消、非法来源/路径、Windows junction。CLI/doctor/API 复用同一结果，HTTP 查询参数不能扩大绑定范围。Linux 可执行位测试已加入 CI，本轮仅做交叉编译，没有在 Linux 执行。
- Node 原只读状态 5 项、源管理状态 40 项、桥接异步状态回归及 Demo 58 项。桥接回归覆盖单请求、drift/失败展示、旧结果清除、错误重试、文本安全、只读请求和正常关闭。
- Codex 内置浏览器验收合成目录 `.artifacts/bridge-final/`：独立只读入口、批准基线一致保留 UNKNOWN、3 个文件的文本/二进制摘要、未选包排除、二进制修改报告 drift、外部源消失清除旧候选、恢复目录后重新检查、会话刷新与退出。新增源页面入口也验收；未记录页面 console error。
- 磁盘核对：应用未覆盖外部修改（测试附件保持 `0002FE`），未创建 `home` 或项目 `.aidock`，登记基线未改变。所有验收仅使用仓库夹具、临时目录和 `.artifacts/`；`E:/work`、`E:/work2–5` 未读取、未写入。

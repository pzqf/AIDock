# 副本候选检查与发布门槛实施

日期：2026-10-14。版本：v0.17.0-dev。仍在阶段 1D，不声明阶段 1 或完整 1D 完成。

跨副本生成候选过去只有编译/恢复清单，缺少逐目标的候选发现与发布资格解释。本轮增加静态预检、显式隔离发现、持久记录与过期核验，并把尚未完成的正式发布门槛直接显示在管理界面。

## 已实现

- 检查输入是已暂存的生成清单完整摘要；发布者和各目标的源、登记、本机排除、分支、基线、工具目录与适配器在整批前后重新核对。
- 从各目标自己的候选完整包重新编译，对照清单全部生成结果。仅重新计算清单摘要不能把被改写的包名、路径或字节作为合法探测输入。
- 默认静态检查不启动工具。显式探测按目标串行使用 Local 下的独立临时项目与空工具配置；读取精确版本、初始化发现，核对待生成包名称/描述/路径，沿用原生进程组/Windows Job 清理。完整二进制附件、空目录和 frontmatter 保留。
- 整批生成有阻断项时不启动任何工具，仍返回每目标静态诊断与具体未验证原因。探测中变更或取消时不保存本次结果为有效证据。
- 发布门槛区分生成候选、原生文件语义、候选发现、实际会话加载、外部生成器验收和正式发布总验收。`candidate_status` 与 `publication_status` 独立；即使前者 PASS，后者仍 BLOCKED，`write_supported=false`。
- 手写项及本机排除包不复制到探测项目、不进入发现覆盖；排除项的保留不误判为待安装语义失败。没有待生成启用包时只核对工具版本，发现初始化标为 SKIPPED，保留项不因此取得发现证据。
- 检查记录保存在 Local 的 `replica-resolutions/<项目作用域摘要>/checks/`。有效期最多 24 小时；show 只读重新核对源、目标、绑定、分支、策略、基线、适配器及程序字节。检查规则绑定为 `owned-candidate-inspection-v1`，规则变化或旧记录缺规则绑定时保留历史并标为 STALE。缺原项目/原计划也不能沿用旧 PASS。
- 读取历史列表不启动工具，不遍历所有历史项目；列表注明历史观察的有效性待核对。旧源/原生资格报告及预演格式不变，新报告不能用于 `native plan --check`。

## CLI / API / 界面

```powershell
# 按已有专用测试绑定附加 --managed-root/--home/--project/--scope
aidock replica generation check run --plan <生成 UUID> --digest <完整摘要> --json
aidock replica generation check run --plan <生成 UUID> --digest <完整摘要> --probe --json
aidock replica generation check show --check <检查 UUID> --json
aidock replica generation check history --json
```

run/show 保留完整 JSON；总证据未完整返回 EVIDENCE_INCOMPLETE/3，候选阻断返回 STATIC_CHECK_FAILED/5，记录过期返回 CHECK_STALE/3。读取历史成功返回 0。退出 3 不是检查进程没有完成，需读取 JSON 中的逐副本结果；不能把退出 0 的历史列表当作发布资格。

API：`POST /api/replica/generation/check`（plan_id/digest/probe），`POST /api/replica/generation/check/show`（check_id），`GET /api/replica/generation/check/history`。沿用会话、Host/Origin/CSRF、严格字段解析与同一后端。

界面：跨副本比较后选择工具，预览处理方案或生成物修复；关闭预览后使用「发布预检与候选发现」。显式探测需确认，取消不创建探测项目。忙碌、切目标、换工具或改处理方案阻止重复或迟到结果污染当前候选。正式发布按钮保持关闭，当前检查不触发源安装、原生应用或副本事务。

## 验证记录

验证只使用 `.artifacts/replica-experience-candidate-checks-v017-20261014` 和 Go 临时目录。五个真实工作目录本轮未扫描、未修改。

| 验证 | 本轮结果与边界 |
| --- | --- |
| 最终源码全仓 | `GOMAXPROCS=2 go test -p 1 -race -count=1 ./...`、`go vet -p 1 ./...`、Windows 构建通过；未推送/触发远端 CI |
| 有意义的负向/持久测试 | 静态不启动进程、完整不可变候选、二进制/空目录/frontmatter、不能成为原生安装资格、程序变化/时间过期、源/目标/发布者/排除策略变化、原计划缺失、规则变化、修改后重算摘要的非法清单、整批阻断不探测、探测中变化与取消、不保存过期整批证据通过 |
| CLI / HTTP | 新命令 run/show/history 与 API 共用后端，默认静态、完整摘要、JSON/退出码、会话/CSRF/严格字段及方法约束通过 |
| 前端 | 8 组状态回归通过；旧 Demo 58 个替代 DOM/timer 回归通过，不充当真实集成验收 |
| 普通源传播候选的真实发现 | 三个目标 × 两工具，6 个待生成启用包发现 PASS。副本 4 原源仍停用，候选启用后可以被发现，证明检查采用暂存候选而非重新读取原源 |
| 纯修复候选的真实发现 | 四个目标 × 两工具；副本 2/3 共 4 个发现 PASS，排除副本 5 和停用副本 4 共 4 个结果 SKIPPED，只核对版本，无待生成包时未运行初始化，不声称排除/手写包已验证 |
| 当前精确版本 | Claude Code 2.1.257、Codex CLI 0.162.1；仅初始化隔离发现，L3/L4 均 UNKNOWN |
| 内置浏览器 | 默认静态、显式探测确认/取消、阻断/未验证区别、关闭预览后候选保留、重启读旧历史、规则缺失旧记录过期、有效记录重开、页面草稿保留通过；480px 页面和矩阵无横向溢出，临时视口已复位，最终页面 warning/error 为空 |
| 字节/副作用 | 五专用副本和共同基线 77 文件 SHA256 + 98 目录保持一致；临时探测项目清空，未创建原生安装资格或副本预演事务 |

持久真实检查：普通传播 `b8f8de07-a327-4a09-9896-7c47d2d61472`；纯修复 `3db45476-d196-4580-81b1-6ddca724bb50`。独立 CLI 进程重新核对纯修复记录，candidate PASS / publication BLOCKED，按证据边界返回 3；默认静态新检查为 UNKNOWN，历史可读取。

本机验收辅助记录与截图位于 `.artifacts/`，不提交：`verify-candidate-checks.py --reports` 核对原件及 14 个工具结果与完整包清单；`verify-check-cli.ps1` 核对独立进程 CLI；`aidock-candidate-checks-v0.17.png`、`aidock-candidate-checks-v0.17-narrow.png` 留存最终界面。检查仅覆盖候选待生成包，不覆盖工具会话/实际能力或 OS 沙箱。

## 继续实施的门槛

一个外部生成器的锁定暂存/恢复适配、正式副本发布/整批故障恢复，以及实际项目新会话加载、L3/L4 和 OS 隔离仍未完成。此次检查是这些门槛之前的候选证据，不是外部生成器或发布降级授权。固定设计 v0.24、原生检查资格和已存在的回退/撤销路径保持各自契约。

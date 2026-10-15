# Profile 批量管理与合并预检 · v0.24.0-dev

日期：2026-10-15。软件增量沿用冻结设计 v0.24，不修改设计归档；阶段 1 尚未整体验收，持续 Goal 保持 active。

## 已开放流程

「Profile 与规则」可以编辑当前共享 Profile 名称、说明、整批技能开关、有序多行规则和 rules_override。关闭窗口保留当前标签页草稿，取消预览不写源；重新加载草稿需要显式二次放弃。编辑期间原 Profile 字节变化返回 PLAN_STALE，不覆盖外部修改、不丢草稿。批量操作只修改 profile.yaml，完整技能包及 pins 不变。

保存复用现有源事务，先预览整批变化，再确认完整摘要；源锁、写前快照、失败/中断恢复、时间线回退与撤销沿用同一实现。整批变化只有一个源恢复点。它不应用到工具、不发布到副本。

CLI：

```powershell
aidock profile show <源绑定参数> --json
aidock profile plan --file <编辑请求.json> <源绑定参数> --json
aidock skill apply --plan <计划UUID> --expected-digest <完整摘要> <源绑定参数> --json
aidock profile resolve --file <显式层描述.yaml> --json
```

编辑请求含加载时 expected_sha256 和完整 profile 对象。GET /api/profile/state、POST /api/profile/plan 以及既有 POST /api/source/apply 提供等价 API，保留绑定校验、严格 JSON、方法、Origin 和 CSRF 保护。

## 确定性合并与当前边界

纯合并器只处理显式 defaults/profiles/project/local/session 和 catalog；按配置链处理开关、精确 pin、递归对象、数组/标量/null 替换、JSON Pointer 删除和有序规则。拒绝同层冲突、缺父/继承环、未知身份、不可取得修订和模糊删除。列表去重并按身份排序，规则不隐式去重；输出层摘要、有效值、覆盖来源和稳定摘要，不修改输入。

catalog 是调用者提供的身份与可取得修订声明，不代替完整包验证、授权或运行隔离。当前源编辑只解析单个已保存共享层；全局 extends 和副本本机覆盖的持久文件关联尚未接入。临时会话层不持久保存。不能将离线合并器描述为完整 Profile 集成完成。

规则、插件、MCP 或原生配置覆盖尚未编译时，源报告给出 PROFILE_NATIVE_UNSUPPORTED，工具预览转为阻断，防止技能安装器静默忽略整份 Profile 的其他内容。规则保存可以回退，但原生规则文件/托管区块、漂移导入/协调仍待实施。

## 验收证据

- 全仓 race：455 PASS、4 SKIP、0 FAIL；13 个含测试包完成。日志 .artifacts/profile-v024/go-race.jsonl。两项 symlink 测试因本机创建权限跳过，两项真实 CLI 探测集成按 opt-in 跳过。最后文案调整后补跑五包 focused Profile 测试通过；未触发远端 CI。
- 13 组前端状态脚本、全部 JS 语法、冻结 Demo 58 项、go vet ./...、Windows 构建和 Linux/amd64 交叉构建通过。Linux 未运行；没有新增真实工具/模型会话证据。
- 单元/事务覆盖六个实际配置层、来源/摘要稳定、规则顺序、数组与转义删除、负向输入；整批只修改一个 Profile、旧摘要拒绝、未知身份整批拒绝、失败恢复、外部修改阻断、回退及撤销。
- 内置浏览器独立项目：两个技能整批启用、名称/说明/多行规则预览取消/保存、关闭重开草稿、重启持久化、外部字节变化 PLAN_STALE 且草稿保留、整批回退后完整 .aidock 与编辑前逐字节相同、撤销恢复两技能与规则。两个工具对未编译规则均显示阻断；未创建 .claude 或 .agents 目录。
- 680px 页面与 Profile 窗口无横向溢出，最终浏览器 error 日志为空。截图 .artifacts/aidock-profile-v0.24.png、.artifacts/aidock-profile-v0.24-narrow.png；数据仅在 .artifacts/profile-v024 和 t.TempDir()。未访问或修改五个真实工作目录。

## 继续实施

下一项补规则原生编译与托管区块的预览/手写保护/恢复。随后继续 Profile 持久关联、完整依赖/兼容、L3/L4、隔离与凭据/审计门槛、外部生成器与正式副本发布、1E，以及阶段 2–5。此增量不将阶段 1 或 Goal 标为完成。

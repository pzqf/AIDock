# 配置分层持久关联 · v0.29.0-dev

日期：2026-10-15。继续冻结设计 v0.24 的阶段 1B/1C/1D，Goal 保持 active。

## 当前行为

配置按显式绑定的全局默认 → 命名 Profile extends 链 → 项目共享 → 本机覆盖确定性合并。全局默认复用库 profile.yaml 和文件规则；可复用 Profile 保存在库 profiles/<名称>.yaml，允许继承。项目通过 Profile.extends 或 project.profile 选择同一名称，不同引用、缺父、继承环、重复及不安全名称明确阻断。只有全局库能编辑命名集合，项目只引用。

旧 --library-root 和全局技能采纳继续保留原来的技能源语义，不会自动改变配置默认值。用户在本机层明确关联库路径，预览确认后持久保存该关联，重启按项目/作用域/副本身份恢复。清除本机覆盖保留库关联；解除库关联必须明确提交空路径。无法取得的完整包 pin 不会自动替换为全局新版。

本机覆盖保存在 Local Root/profile-overrides/<绑定摘要>/ 的 binding.json 与可选 profile.yaml；独立恢复资产在 profile-override-transactions。绑定包含项目、副本、作用域和源根，拒绝自动重绑，未知文件保留并阻断；本机层不能引入 extends。没有明确 copy 的旧调用不能写本机层。

本机保存、清除、最近事务回退和再次回退（撤销）共用独立持久事务。完整备份、确认摘要、源/原本机/旧库/候选库观察、失败恢复和外部修改保护继续生效。未完成本机事务不会让恢复命令因半写绑定而不可达，正常编译仍暂停等待恢复。本机文件不进入共享技能目录或跨副本源候选。

## 界面与执行输入

「Profile 与规则」支持共享/本机切换，未保存草稿阻止切层；技能开关区分继承、明确启用和明确停用。全局入口可新建和编辑命名 Profile、继承和规则；项目选择引用；本机层可关联库、编辑规则、清除及回退/撤销。已保存和候选结果分别显示有效值、来源和覆盖顺序，恢复入口独立常驻，技术原始报告可展开。

源预检、普通导出、不可变副本候选和两工具托管规则均使用合并后配置。共享列表仍显示共享开关，并另提示本机遮盖，避免共享比较混入本机偏好。原生计划与检查绑定配置摘要，执行期间锁定对应显式层；源计划也绑定配置层观察。

副本读取现在恢复各目标 copy 的本机配置。内置和外部返回包暂存都保存目标配置摘要；确认、检查和历史有效性核对重查本机/库输入，描述变化也会使旧结果过期。当前副本暂存只协调技能目录；合并后包含规则时返回 RULES_TARGET_REQUIRED 的 FAIL finding，阻止预演，不能静默丢弃规则。规则目标协调与正式发布仍待完成。

未具备原生编译能力的 config_overrides、受管 MCP 等继续 UNKNOWN/阻断安装，不以层解析通过替代实际应用。临时会话层仍只由 profile resolve 显式输入，不持久化为本机配置。

## CLI/API

profile show 返回 profile、layers、report 和本机 history；全局调用另返回 named_profiles/named_sha256。profile plan --file 支持 target、expected_layers_digest、library_root、clear、snapshot 和命名集合完整编辑。共享确认兼容 skill apply；本机确认用 profile apply --target local --plan <UUID> --expected-digest <摘要>，恢复用 profile recover-local。

API 为 /api/profile/state、/api/profile/plan、/api/profile/apply 和 /api/profile/recover-local，继续使用同一来源与事务、当前请求绑定、方法/Origin/CSRF 校验。命名集合编辑要求 expected_named_sha256；本机 expected_sha256 是完整本机记录树摘要。

## 验证

- 全仓 race：517 PASS / 5 SKIP / 0 FAIL，14 个测试包；日志 .artifacts/experience-profile-layers-v029/go-race-delivery.jsonl。5 项 SKIP 是既有平台/显式集成条件限制，未当作通过。
- 13 组前端状态回归、58 项 Demo 回归、JS 语法、go vet、Windows 构建与 Linux/amd64 交叉构建通过；Linux 未运行。
- 新回归覆盖持久合并与规则顺序、共享/本机身份隔离、本机清除/回退/撤销、失败恢复、未知文件和继承错误、晚到输入修改、CLI/API，以及逐副本本机开关、内置/外部暂存过期与规则阻断。
- 内置浏览器在独立体验项目完成全局默认、两级命名继承、项目文件规则、库关联取消/保存、本机停用和规则、独立回退/撤销。最终二进制重启后五层、五条规则、四条本机记录保留。
- 浏览器两工具预览均只包含合并后唯一启用技能，五条规则正文一致；四目标独立预演应用/回退/撤销通过。390×844 页面无横向溢出，最终页面 console error/warn 为零。
- 磁盘核对：共享源 13 文件摘要不变，本机回退再撤销后 2 文件逐字节一致，绑定原生目录与规则文件未被预览/预演写入。证据与截图见 .artifacts/experience-profile-layers-v029/acceptance.json。

本轮浏览器没有实际绑定项目安装或模型请求。Go 中原生安装集成沿用受控探测，不把它当作新的实际项目 L3/L4 证据。未访问或修改五个真实工作目录。

## 剩余必做

继续 Claude Code 规则加载、实际项目 L3/L4、凭据与审计、OS 隔离剩余门槛、受管 MCP 启动、外部生成器执行、逐副本规则目标协调与正式五副本发布、1E，再进入阶段 2–5。阶段 1 与整体 Goal 尚未完成。

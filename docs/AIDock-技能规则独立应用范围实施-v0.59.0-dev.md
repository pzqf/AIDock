# 技能与规则的独立应用范围

版本：v0.59.0-dev。项目启用 MCP 后，技能与规则应用会被 MCP 的运行、来源和评估诊断一起阻断。现在可明确选择仅技能与规则的范围，保留默认完整 Profile 检查；MCP 仍走独立连接安装与恢复流程。这不是全配置联合事务，阶段 1 和完整 Goal 均未完成。

## 使用方式

AI 工具页新增“应用内容”：默认“完整 Profile 检查”，可选“仅技能与规则 · MCP 独立管理”。切换范围使原检查、预览和确认失效，保留当前安装和未保存草稿。检查确认、结果、预览及安装确认均显示范围；范围外 MCP 诊断独立保留，不计为本次阻断，也不据此显示 MCP 已生效。

仍需显式确认启动空配置独立项目的工具发现和规则来源检查；预览后另行确认安装，自动备份并可回退/撤销。无文件变化时禁用重复安装。完整检查遇到 MCP 条件时给出独立范围与 MCP 管理的下一步；规则被阻断而尚未编译时显示等待处理，不误报本次无规则。

CLI 的 `tool prepare`、`tool check`、`plan` 新增 `--application-scope full|skills-rules`，缺省 full；原 `--scope` 继续表示项目作用域。沿用既有项目绑定参数：

```text
aidock tool prepare --all --application-scope skills-rules --probe --rule-probe
aidock diff --plan PLAN_ID --digest DIGEST
aidock apply --plan PLAN_ID --digest DIGEST
```

API 的 `/api/native/prepare`、`/check`、`/plan` 使用 `application_scope`。full 是默认，持久记录空值沿用完整范围；skills-rules 明确持久化。其它值拒绝。`tool check --last` 按记录原范围核对，不接受附加范围选择。认证、CSRF、Origin、固定项目绑定和严格请求解析继续保留。

## 摘要和事务

只有源编译器标注 `component: mcp` 的 Profile 诊断可进入范围外清单，不按代码前缀或用户字段跳过。技能自身的插件依赖、执行需求、未知原生元数据、规则来源、目标碰撞、一般 Profile 失败及未实现设置仍阻断。

范围进入 Plan、CheckReport、Qualification 及摘要。Diff、Apply、Latest 按记录原范围重编译，不同范围的发现检查不能交叉授权。Plan.Source 保留完整原始 Export，没有删改 MCP 或配置摘要；预览后的 MCP 定义变化仍使确认失效，最后写前持锁重新比较完整 Export。

本次目标只含两工具的技能目录与规则文件，继续使用持久 Batch、项目锁和恢复屏障。可能含凭据的 MCP JSON/TOML 不进入普通技能事务。MCP 配置及归属侧车保留原字节，仍由 v0.58 的私有备份事务管理；未恢复事务照常阻断其它写入。

## 验收

证据目录：`.artifacts/experience-application-scope-v059/`，汇总 `verification.json`。

- native/source/inventory/workstore/cli/console 六包完整竞态退出 0；最后的依赖、执行需求、未实现设置负向及准备接口经三包定向竞态再次退出 0。非竞态五包回归退出 0。
- 26 组前端状态测试、26 JS 语法、gofmt、go vet、Windows 构建、Linux 交叉构建通过；Linux 未运行。
- 新项目 10 项真实 CLI 验收：先安装双工具受管 MCP，再启用自定义技能和规则，验证完整范围阻断、独立静态预览、两工具隔离发现/规则来源、跨范围拒绝、预览后外部编辑拒绝、安装、幂等、回退及撤销。源/产物全树及 MCP 三文件原字节保持。
- 6 项认证 API 验收通过，覆盖两个范围、跨范围拒绝、无效值、原摘要 Diff 和不写目标。无模型请求，L3/L4 均保持 UNKNOWN。
- 内置浏览器完成取消、切换范围、检查、安装、回退、撤销和幂等禁用。未保存的 Profile 规则草稿在安装和恢复后保留；磁盘比对确认工具内容变化及 MCP 原字节保留。

首夹具漏启用技能，正确得到 L2 SKIPPED，不能算技能发现通过。初始脚本和日志保留；修正启用步骤及回退命令后，另建全新项目完整重跑通过。没有访问或修改 E:/work、work2–5。

## 剩余工作

技能、规则、MCP 尚无一个联合提交。MCP 保留现有授权、版本、安装与运行门槛，有效配置变化可能使旧授权/回执失效，本次范围选择不恢复授权。同绑定单实例、Tier 0 非隔离、逐次写批准、Tier 1/2、出口 broker、外部生成器实际执行、正式五副本发布及阶段 2–5 仍在计划内。实际项目的新会话正文和能力生效需继续验收，阶段总验收不提前标勾。

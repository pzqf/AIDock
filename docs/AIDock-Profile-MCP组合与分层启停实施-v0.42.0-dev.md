# Profile MCP 组合与分层启停实施

版本：v0.42.0-dev。状态：完整增量已验收，阶段 1 和整体 Goal 继续。

## 用户行为

在「Profile 与规则」的 MCP 组合中，可以对项目共享、全局默认、命名 Profile 或当前副本本机层选择继承、明确启用、明确停用。预览显示本层变化和候选有效值，确认后保存；取消与关闭保留草稿。

有效值按 MCP 定义默认 → 全局默认 → extends 链 → 项目共享 → 本机覆盖 → 临时会话依次计算。界面显示保存的有效值及覆盖来源，命名 Profile 在项目引用后按继承链合并。MCP 定义页分别展示定义默认与当前有效启停。

有效启用的 MCP 仍进入安装、项目授权、隔离及受管运行检查；有效停用的 MCP 退出工具应用检查范围，但定义页继续显示完整诊断。任何 Profile 保存都不启动 MCP、不授予权限、不生成原生启动配置。

## 契约与保护

- 纯合并输入增加可选 mcp_defaults，给定义默认独立的 mcp-definition 层和 trace。当前项目自己的定义名称进入 Catalog；全局引用不会隐式拷贝定义。
- profile show 增加 mcp、mcp_sha256，profile plan 接受可选 expected_mcp_sha256；界面始终发送，保留旧 CLI 请求兼容。
- mcp list 增加 selection_digest、profile_report；服务器区分 enabled（定义默认）、effective_enabled、selection_resolved、enabled_source 与 selection_trace。mcp plan 可绑定 expected_selection_digest。
- 未知 MCP 引用不能保存。已关联共享、全局、命名或本机 Profile 引用的定义不能删除或改名，须先解除引用。损坏定义可显式替换修复，但不能遗失已有引用。
- 定义、配置层或来源变化使旧预览失效，保持未保存草稿。当前有效值相同但覆盖来源变化也不能复用旧确认。
- 本机层使用独立持久事务与恢复链，支持清除、回退和撤销，不改共享源、不传播到其它副本。临时会话为纯显式层输入，未伪装成已保存状态。

## 验证

Windows 全仓竞态：806 PASS / 7 SKIP / 0 FAIL，18 包，退出 0。新回归覆盖六层来源、未知默认、输入不变、安装检查范围、引用删除保护、损坏定义修复、重启、本机清除/回退/撤销、跨副本隔离、来源改变使旧计划失效；CLI/API 和原生预览使用相同有效结果。

20 组前端、58 项 Demo、21 个 JS 语法、gofmt、go vet、Windows 构建与 Linux 交叉构建通过；Linux 未运行。

独立浏览器实例完成：

1. 共享 MCP 继承预览、取消、关闭重开草稿和二次确认重读；未保存共享变化。
2. 本机启用/停用、取消/确认、回退/撤销、清除覆盖及回退清除；本机 search 最终启用、memory 最终停用。
3. 定义默认与本机有效值相反的两种显示，以及全局命名 Profile 的编辑/预览/取消草稿；未保存全局变化。
4. 预览后 CLI 独立修改本机层，旧确认返回 PLAN_STALE；草稿名称保留，二次确认重读后显示外部保存的说明。
5. 重启保留记录；390×844 窄屏页面、对话框和选择控件均无横向溢出。最终构建无浏览器 warn/error。

最终摘要核对：项目源 6 文件和全局库 6 文件逐字节不变；本机历史 9 条，最后一条为并发验收的独立修改。未访问五个真实工作目录。

证据目录：.artifacts/experience-profile-mcp-v042。主要文件为 verification.json、go-race.jsonl、frontend-final.json、acceptance.cjs、acceptance.json、profile-mcp-desktop.png、profile-mcp-narrow.png 和 profile-mcp-stale.png。

## 剩余任务

继续安装获取、真实评估服务和在线刷新、项目授权、stdio 中继、出口 broker、凭据与 OS 隔离、会话生命周期及原生配置接入。MCP 定义/锁的副本同步、正式发布、L3/L4、1E 和阶段 2–5 仍待完成，不提前标记整体可用。

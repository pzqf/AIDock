# AIDock 依赖与兼容预检实施记录 · v0.5.0-dev

日期：2026-10-13。此记录只覆盖阶段 1B 的治理声明与静态预检子集。

## 已交付

- 每个 owned 技能可保存 `dependencies`、`compatibility`、`requirements` 三个治理对象；三者参与新的 semantic-v1 revision，旧 revision 的完整字节和恢复记录保持不变。
- 技能依赖使用 owner/scope/id/revision/required 精确绑定。缺失、修订不符、退役、重复和必需依赖环分别给出结果；可选依赖不会自动启用或下载。
- 预检读取当前显式绑定的完整包和声明，验证包闭合、文件摘要、Profile pin、平台、脚本存在性和 `exec` 声明；插件、运行时、项目文件、工具版本、动态脚本依赖和隔离能力没有运行证据时显示 `UNKNOWN`。
- CLI `skill check`、控制台预检面板和 `doctor` 复用同一份报告。启用计划会重新预检；存在静态 `FAIL` 时返回 `SKILL_PREFLIGHT_FAILED`，不写源。候选预检带 `candidate=true`，不会被显示成已生效。
- 治理编辑支持 JSON 草稿、固定 revision 过期保护、继承包只读、显式放弃草稿、源事务回退和撤销。治理记录按 revision 保存，不能改写旧记录。

## 边界

预检是源和声明层证据，不是工具应用、生成器、会话加载、插件执行、网络访问或 OS 隔离证据。工具版本尚未接入支持白名单，项目外依赖需后续登记；外部原源桥接、完整 extends/local overlay、跨副本发布、审计哈希链和物理删除仍未实现。

## 验证

- Go race 全仓通过；新增治理、依赖、回退、错误输入、CLI 和控制台测试通过。
- Node 状态测试 40 项，Demo 回归 58 项通过。
- 内置浏览器合成项目验证：继承治理只读、无效 JSON 草稿保留、候选治理预览/保存、新 semantic revision、必需依赖未启用时阻断，以及显式启用依赖后允许消费者启用。
- 测试只使用 `.artifacts/` 合成项目；`E:/work`、`E:/work2–5` 未读取、未写入。

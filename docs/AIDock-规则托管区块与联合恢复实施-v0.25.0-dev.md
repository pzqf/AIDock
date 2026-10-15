# 规则托管区块与联合恢复 · v0.25.0-dev

日期：2026-10-15。软件增量沿用冻结设计 v0.24，不修改设计归档；阶段 1 未完成，持续 Goal 保持 active。

## 已实现

已保存 Profile 的有序多行规则编译为项目 CLAUDE.md / AGENTS.md 的 `<!-- aidock:begin -->` 与 `<!-- aidock:end -->` 区块。规则不重排、不去重；区块外原字节（BOM、CRLF、空行及手写段落）保留。首次追加仅增加必要分隔换行；归属存于对应技能目录原有 .aidock-ownership.json 的可选 rule 字段，含目标路径与托管摘要。

工具页面显示托管正文及只读手写内容。既有 plan/diff、tool rehearsal run/show/plan/apply/recover 与对应 API 复用同一实现，不引入另一套写入逻辑。独立预演同时处理两技能目录、两规则文件，四目标共用写前完整快照、实际目标锁、计划过期检查、持久 journal/marker、失败恢复、回退与撤销。单文件快照只捕获该文件，不扫描父目录或兄弟文件；旧目录事务/摘要兼容。

未登记的已有区块不自动接管；托管区块手改、丢失、标记重复/嵌套/缺失、非法 UTF-8/NUL、规则源含保留标记均阻断整批应用。空规则清空已归属区块正文并保留手写内容，不删除整文件。回退遇文件的任何外部修改会报冲突，不自动合并。仅有规则、没有启用技能时也建立明确的空 packages 归属。

## 验收证据

- 最终全仓 race：485 PASS / 4 SKIP / 0 FAIL，14 个含测试包完成；日志 `.artifacts/rules-v025/go-race-final.jsonl`。两项 symlink 权限和两项真实 CLI opt-in 跳过。初轮发现空技能状态缺少归属的回归，已修复并重新通过；初轮失败日志保留，不能作为通过证据。
- 13 组前端状态回归、全部 JS 语法、冻结 Demo58、go vet、Windows 构建与 Linux/amd64 交叉构建通过。Linux 未运行、远端 CI 未触发；没有新增真实工具/模型会话加载证据。
- 单元/事务含手写字节保留、归属/漂移/标记/编码负向、幂等、规则清空与规则独用、摘要过期、第二目标失败、staged 写入、真实子进程 exit95、中断及恢复冲突、回退/撤销和旧历史引用兼容。CLI/API 对四目标规则联合恢复等价验收。
- 内置浏览器独立测试项目：保存有序多行规则；重启后持久；取消预演零写入；真实预演四目标12变化；回退后只剩原 CLAUDE.md 且与 BOM/CRLF 备份逐字节一致；撤销后整个预演项目逐字节恢复。原 .aidock 与已绑定原生目标保留。
- 预览后测试规则文件外部编辑返回 PLAN_STALE；未登记合法区块返回 RULES_OWNERSHIP_REQUIRED。负向测试后测试文件恢复原字节。680px 页面与规则面板无横向溢出，最终 console error 为空；重启后恢复链 UNDONE 正确显示。
- 截图 `.artifacts/aidock-rules-v0.25.png` 与 `.artifacts/aidock-rules-v0.25-narrow.png`；验收摘要 `.artifacts/rules-v025/acceptance.json`。数据只在 .artifacts 与 t.TempDir()，未访问或修改五个真实工作目录。

## 保持关闭与后续

RULES_SESSION_UNVERIFIED 继续阻断正式规则安装；技能发现通过不等于规则会话加载。Profile 源规则检查为 PROFILE_RULES_PENDING；插件/MCP/config 仍明确 unsupported，技能专用副本生成与外部生成合同不会静默遗漏规则。

规则漂移的显式导入/覆盖/合并、.aidock/rules 文件化规则库、全局 extends 与本机覆盖持久关联、完整依赖/兼容、L3/L4、隔离/凭据/审计、外部生成器执行与正式副本发布、1E 与阶段 2–5 尚未完成。继续这些工作，不将本增量或阶段 1 标为 Goal 完成。

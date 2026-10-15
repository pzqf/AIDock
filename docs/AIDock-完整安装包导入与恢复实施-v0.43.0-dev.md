# 完整安装包导入与恢复实施

版本：v0.43.0-dev。状态：完整增量已验收，阶段 1 和整体 Goal 继续。

## 用户流程

在「MCP 定义 → 安装锁与本地产物 → 导入完整安装包」选择已锁定 MCP，填写本机完整 ZIP 的绝对路径，预览文件与目录变化后确认。ZIP 内直接对应已保存 plugins.lock 的清单，不加外层目录。目标须缺失或为空；已有内容保留并阻断覆盖。

完整包导入后，可继续检查清单、来源和安全证据，再独立登记本机安装。导入不登记安装、不执行代码、不提升来源信任或授予运行权限。本轮不联网取包、不运行包管理器或安装脚本。

每次成功导入保留独立历史，可预览回退、撤销最近操作。进程中断后使用同一项目、作用域、源根和产物相对目录恢复；当前源锁损坏仍可恢复原事务，不依赖重新解析锁。外部修改被保留，冲突明确阻断。存在任一项目的本机安装登记引用时，先解除相关登记才能回退产物。

## 契约与保护

- CLI：mcp artifact state / plan --file / apply --name --plan --expected-digest / recover --path。
- API：GET /api/mcp/artifact/state；POST /api/mcp/artifact/plan、apply、recover。鉴权、CSRF、严格字段和方法检查沿用控制台约束。
- 导入请求包含 action=import、name、expected_sha256、expected_lock_sha256 和 archive_path；回退为 action=rollback 与 snapshot。确认绑定项目、作用域、源根、完整源摘要、锁摘要、目标观察与完整 ZIP 摘要，任何相关输入变化使旧确认失效。
- 核对完整文件、隐藏文件、空目录、运行时、依赖、字节、摘要、模式和平台；拒绝缺件、多件、重复路径、越界、链接和摘要不符。
- 同一 Local Root 的所有项目安装引用都保留产物。安装登记与包导入/回退共用实际产物目录锁，确认时在锁内重新核对引用。
- 仅内部 plugin-artifact 事务允许 256 MB 单文件与 512 MB 总量；普通源和元数据继续保持 10 MB 单文件与 256 MB 总量。逐文件替换前读取当前文件，结束后完整 capture 核对，避免每写一文件扫描整个依赖包。
- 独立持久事务、原字节备份和恢复保护复用 workstore；RecoverGuarded 在目标锁内核对原绑定。未知或外部修改不被静默覆盖。
- 界面预览取消、关闭重开和过期均保留路径草稿；导入入口位于安装锁页顶部，历史时间按本地格式显示，回退摘要区分写入和移除数量。

## 验证

初始 Windows 全仓竞态：830 PASS / 8 SKIP / 0 FAIL，18 个有测试包，退出 0。随后补充跨项目引用与实际目录锁，六个受影响包完整竞态 329 PASS / 1 SKIP / 0 FAIL，4 个有测试包，退出 0。最终增加原绑定恢复 guard 后 source/workstore 聚焦竞态 23 PASS / 1 SKIP / 0 FAIL，退出 0。最终代码未再次完整重跑全仓，以上证据分别保留。

回归覆盖 11 MB 二进制、隐藏文件和空目录；非法 ZIP、未知目标保护、源/锁/目标/包变更；同项目及跨项目安装引用、目录锁争用；真实测试子进程 exit94 分别在替换后和大文件 staged 后中断，新进程恢复；锁损坏仍恢复、错误作用域拒绝恢复和外部修改保留。CLI/API 完整导入、回退、撤销、严格输入与请求保护均通过。

最终前端 21 组、22 JS 语法及 58 项 Demo 通过；gofmt、go vet、Windows 构建和 Linux 交叉构建通过，Linux 未运行。

内置浏览器独立项目完成缺隐藏文件拒绝、预览取消与路径草稿、ZIP 外部修改导致旧确认过期、5 文件/11534643 字节/8 项导入、回退及撤销、安装登记引用阻断和重启保留 3 条历史。390×844 检查页面 390/390、对话框 331/331、输入 290/290，无横向溢出。最终 Windows 构建另行复核历史、回退预览及取消，warn/error 为 0。

独立验收脚本最终核对 7 个项目源文件摘要不变、5 个产物文件与完整包一致，当前安装登记 0、安装审计 2，来源保持 L0、execution_ready=false。未访问五个真实工作目录。

证据：.artifacts/experience-mcp-artifact-v043/verification.json、go-race.jsonl、go-race-final-affected.jsonl、go-race-final-binding.jsonl、frontend-final.json、acceptance.cjs、artifact-desktop-final.png 和 artifact-narrow.png。最新 Windows 验收构建为 aidock-verified.exe。

## 剩余任务

继续项目授权、stdio 中继、出口 broker、OS 凭据库与隔离、会话生命周期和原生配置接入。联网取包与依赖解析、真实评估服务/在线刷新、MCP 定义与锁的副本同步、正式发布、L3/L4、阶段 1E 和阶段 2–5 仍待完成。

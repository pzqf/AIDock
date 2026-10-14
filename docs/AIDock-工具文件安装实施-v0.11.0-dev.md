# 工具文件安装、回退与撤销

2026-10-14，开发版本 v0.11.0-dev；设计基线仍为 v0.24。

本轮把 1C 的正式文件安装入口贯通到 CLI、API 和控制台。只有当前保存版本显式执行工具发现检查，检查未过期、工具程序摘要未变化、精确版本在适配器名单中且没有未验证的技能语义时，才生成可安装计划。确认只安装完整包，不启动工具或执行技能脚本。

支持 Claude Code 2.1.257、Codex CLI 0.162.1 的项目技能文件安装与隔离发现。当前可安装子集只含已验收的 name/description frontmatter；其它原生字段完整保留在源、预览与预演中，但未经对应语义验收时拒绝正式安装。检查记录、源、目标、工具选择和适配器均绑定计划摘要。最后写前在源锁内重查完整导出，包含继承与外部登记；工具实际目标锁和持久恢复复用 v0.10。

```text
aidock tool check --all --probe <相同项目绑定> --json
aidock plan --all --check <检查 UUID> <相同项目绑定> --json
aidock apply --plan <计划 UUID> --digest <完整摘要> <相同项目绑定> --json
aidock tool history <相同项目绑定> --json
aidock tool rollback plan --transaction <最新成功事务> <相同项目绑定> --json
aidock tool rollback apply --plan <回退计划> --digest <完整摘要> <相同项目绑定> --json
```

回退最新回退事务即撤销回退；恢复入口不依赖工具仍安装或原检查仍新鲜，避免失去修复能力。外部修改涉及路径时拒绝覆盖，无关手写文件保留。源回退与工具回退分别操作。

本地验证：全仓 Go race（含两款真实 CLI 的独立目录检查）、vet、构建通过；新增过期/静态检查/版本/工具替换/源变化/工具选择/未知字段负向检查，以及完整包安装、幂等、回退、撤销、手写项保留。前端新增安装确认/取消、重复提交、事务记录和恢复冲突验证；原 Demo 58 回归通过。

内置浏览器在 `.artifacts/experience-native-install-20261014` 验收两个工具检查、正式安装取消/确认、回退及撤销。安装前的 11 个源文件 SHA256 全部不变；取消后两个工具目录均不存在，回退后目录恢复为不存在，撤销后产生 4 个 SKILL.md。真实五工作区未扫描或修改。

边界：L1 可以证明项目文件一致，L2 只证明空工具配置下的独立项目发现。L3 正文加载、L4 模型使用、实际项目/用户配置优先级、受管 MCP 与 OS 隔离仍未验收；原生客户端执行技能属于 external。这不是阶段 1C 或完整阶段 1 的总验收。

官方契约：[Codex Skills](https://developers.openai.com/codex/skills/)、[Codex app-server](https://developers.openai.com/codex/app-server/)、[Claude Skills](https://code.claude.com/docs/en/skills)。

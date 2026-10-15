# 技能管理入门

适用于当前 Windows 开发版。先在独立测试项目使用，阶段 1 的完整验收仍在进行。

## 打开管理界面

在仓库目录运行：

```powershell
go run ./cmd/aidock config --home .artifacts/my-aidock
```

终端运行期间，页面可以创建或打开项目、切换技能库、编辑完整技能包和 Profile、检查工具应用、查看保存与回退记录。关闭控制台会停止本次服务；再次运行相同命令会读取已有数据。若要在 Codex 内置浏览器打开，增加 `--no-open`，再打开终端输出的本地地址。

需要带示例技能的独立项目时：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\start-demo.ps1 -InstanceName skills-test -NoOpen
```

启动器只初始化新的仓库内体验目录，再次运行保留已有修改。真实工作目录不能用于验收。

## 按一个完整流程使用

1. 在“项目”页接入独立目录，然后打开该项目。
2. 在“技能库”创建技能，或导入包含 SKILL.md、附件和脚本的完整目录。先预览，再确认保存；二进制附件保留原字节。
3. 在列表搜索名称、描述或标签，按来源、启停筛选；“版本与来源”展示稳定身份和固定修订。
4. 单项启停使用技能行按钮；多项组合进入“Profile 与规则”，设置共享层或本机层，核对有效结果后确认保存。
5. 在“AI 工具”选择工具，准备并核对安装预览，再确认安装。保存源与安装文件是两次明确操作。
6. 需要恢复时，在“时间线与回退”或工具应用记录预览回退；回退也生成恢复点，可以撤销。未完成事务须先恢复。

“源已启用”说明已保存 Profile 选择。工具文件一致、隔离发现和实际项目会话加载分别检查；当前实际项目 L3/L4 仍未验收。

## 从 CLI 使用同一个项目

先列出登记入口：

```powershell
go run ./cmd/aidock project list --home .artifacts/my-aidock --json
```

复制结果中需要使用的入口 `id`。下面的 PowerShell 参数数组保留同一个 home 和绑定，技能 ID 则使用 `skill list` 返回的稳定 UUID：

```powershell
$aidockBinding = @('--home', '.artifacts/my-aidock', '--binding', '<入口 id>', '--json')
go run ./cmd/aidock skill list @aidockBinding
go run ./cmd/aidock skill show <技能 UUID> @aidockBinding
go run ./cmd/aidock skill show <技能 UUID> --file SKILL.md @aidockBinding

# 仅生成共享开关预览
go run ./cmd/aidock skill enable <技能 UUID> @aidockBinding
# 核对结果后，复制该预览的 plan.id 和 plan.digest
go run ./cmd/aidock skill apply --plan <计划 UUID> --expected-digest <预览摘要> @aidockBinding

# 停用使用同样的预览、确认步骤
go run ./cmd/aidock skill disable <技能 UUID> @aidockBinding
```

`skill list` 返回当前项目完整状态，含项目资产、已采纳继承、外部批准、Profile、历史和待恢复标记；`managed-list` 保留兼容。`show` 只接受当前绑定内的稳定身份，不通过同名技能猜测资产。`show --file` 只读取包内相对路径，二进制只返回摘要等元数据。

`enable/disable` 不直接保存，也不启动工具；它们与 `skill plan enable/disable --file` 共用同一源计划。确认必须带摘要，源或输入发生变化后旧计划失效。当前命令修改共享层，更高层可能覆盖它，请核对预览中的有效配置。

## 全局库和外部来源

全局库使用显式的 `--global-root <库目录> --home <数据根>` 打开。项目在“Profile 与规则”的本机层关联库；可见库中版本后，仍需明确采纳完整固定包。库中新增、改版的技能不会自动替换项目已选版本。

库默认层可以包含尚未采纳的版本引用；若有效配置要启用某个未采纳资产，会明确阻断，需先采纳或在项目/本机层停用。未知身份、缺失或损坏修订继续阻断。

外部批准包只读，原目录保留权威来源。修改原源后重新检查并明确采纳；项目可在批准历史选择完整缓存版本。

原始只读盘点使用另一套明确合同：`skill scan/list/show --manifest <来源清单>` 返回 `candidate:` locator。locator 不是稳定技能 UUID，不能拿它执行源启停；该模式不会自动登记项目。

完整能力及剩余验收见 [开发计划](./AIDock-开发计划.md) 和 [开发进度](./AIDock-开发进度-2026-10-13.md)。

# 主入口联合应用与统一恢复 · v0.66.0-dev

AI 工具页默认使用“技能、规则与 MCP”，一次检查并准备应用，核对后由主安装按钮确认。原有独立联合步骤收进高级区；仅更新技能规则仍可选择原范围。未适配 plugins/config_overrides 字段继续阻断，完整 Profile 与阶段 1 尚未完成。

## 使用与契约

MCP 安装登记、独立项目授权和运行时核验需先完成。点击“检查并准备应用”并确认工具发现，系统在空配置临时项目检查技能与规则，再读取全部有效 MCP 的已有绑定，生成一个完整联合计划。缺少任一 MCP 条件时保留技能规则预览并显示原因，主安装与复核均不可绕过到部分 Profile。没有 MCP 时直接生成技能规则的联合事务，缺失原生配置保持缺失。

CLI：

```text
aidock tool profile prepare --all --probe --rule-probe <绑定参数> --json
aidock diff --plan <联合计划> --digest <完整摘要> <绑定参数> --json
aidock apply --plan <联合计划> --digest <完整摘要> <绑定参数> --json
aidock tool history|recover <绑定参数> --json
aidock tool rollback plan --transaction <最新成功事务> <绑定参数> --json
aidock tool rollback apply --plan <回退计划> --digest <完整摘要> <绑定参数> --json
```

无 --probe 的 prepare 为静态检查，不启动发现或版本进程；不会授予权限、启动 MCP 或发送模型请求。完整准备 API 为 POST /api/native/profile/prepare，严格拒绝重复键、别名、空值和自定义 command。默认主 apply/diff 及原 /api/native/apply、/diff 按固定绑定的计划身份分发。tool history/recover/rollback 及 /api/native/transactions、/recover、/rollback/plan、/rollback/apply 汇总原生技能规则、单 MCP 连接与 Profile 联合三类事务，继续调用原事务校验器。调用者不能通过类型或路径扩展目标。各类独立入口保留兼容。

## 一致性与回退修复

准备只组合既有检查和现有授权，确认时仍持有源/配置/产物及全部权限锁，再核对源、授权、程序、检查时效和目标摘要。联合计划的精确 UUID/digest 传给主按钮；只存在 MCP 变化也可确认。旧技能规则资格不等于完整 Profile 资格。变更后失效、迟到响应与重复提交继续阻断。

修复通用 Batch 的父目录记录：目标 after 不存在时不取得其缺失父目录的所有权。原逻辑会将未创建的可选配置父目录同时放入回退的创建/清理清单，导致无 MCP 项目回退失败。新测试覆盖正常计划和模拟旧计划，应用→回退→撤销→再次回退，缺失可选目录保持缺失且已创建技能父目录可清理。

统一历史按真实时间排序并标注三种应用范围。回退保留原批次“最新成功事务”及文件当前值约束；不能把已被同类后续事务取代的旧引用直接当成最新事务。完整任意历史项目 replay 仍单独待做。

## 验收

专用证据根 .artifacts/experience-main-profile-flow-v066/。真实 Claude Code 2.1.257 和 Codex CLI 0.163.0 验证技能发现与隔离规则来源；CLI/API 默认准备编译相同七个目标，主 apply/diff/history/recover/rollback/undo、MCP 遗漏拒绝、重复应用和目标手改阻断通过。查询与安装未启动插件，source/artifact 保持，七个目标恢复原字节。

内置浏览器默认准备、复核、取消、主安装、统一历史回退/撤销/再次回退通过；390/1280 宽无横向溢出，warn/error 0。独立无 MCP 真实项目通过完整回退循环，原生配置及可选目录保持缺失。

组合前端测试同时加载 NativePreview 和 NativeWrite，覆盖仅 MCP 变化、联合身份传递、取消、主安装/历史/回退、MCP 条件失败及零差异。共 30 个 JS 语法与 31 组状态测试、58 项 Demo、Windows 构建、go vet 与 Linux 交叉构建通过；Linux 未运行。workstore/native/cli/console/source/replica/generator/engine 八包完整竞态退出 0。最终构建又完成安装/回退/撤销/再回退，历史损坏读取清空旧记录并禁用回退，注入原字节恢复后可重新读取。结果见 verification.json。

中间夹具记录保留：首个 setup 漏传 prepare 子命令，未创建业务数据；首轮混合历史清理错误期待能回退已被同类新事务取代的旧引用，被 PLAN_STALE 正确拒绝。已按原约束修正，在本专用夹具复位后重跑，不放宽产品保护。

## 剩余

任意 plugins/config_overrides、完整 Profile 正式副本同步、外部生成器执行/发布、真实技能 L3/L4、网络隔离/出口 broker、顶层完整项目 replay、审计分段，以及阶段 1E 总验收和后续阶段继续。Goal 保持 active。未访问真实 E:\work、work2–5。

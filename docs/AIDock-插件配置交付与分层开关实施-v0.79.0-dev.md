# 插件配置交付与分层开关 · v0.79.0-dev

更新：2026-10-17。Goal active，阶段 1 总验收与 59 条状态不提升。

## 行为

已登记的 MCP 是明确的插件类型映射；Profile 的 `plugins` 与 `mcp` 开关按全局默认、extends、项目、本机及临时层顺序作用于同一资产。同层相反声明返回 `AMBIGUOUS_OVERRIDE`；未登记身份不按同名猜测。没有使用 plugins 映射的历史 Profile，其有效输出及摘要保留。删除定义须先解除插件/MCP 开关与配置覆盖引用。其它插件类型的注册、消费与适配继续开发。

`config_overrides` 与 `config_remove` 先沿用既有确定性合并。当前锁定产物中的 plugin.yaml 必须声明字段与配置交付合同，才可消费覆盖值；未知字段、类型、变量、清单未核验或缺合同都不会获得启动授权。源草稿可保留待协调配置，应用与执行门槛继续阻断。

插件可以显式声明以下合同；只有实际程序能够读取该环境变量时才应声明，不会给既有第三方程序自动加上消费能力：

```yaml
config_delivery: env-json-v1
config_schema:
  languages:
    type: multi-select
    label: 语言
    options: [go, typescript]
    default: [go]
  index_dir:
    type: directory
    label: 索引目录
    default: ${PROJECT}/index
  optional:
    type: string
    nullable: true
```

当前字段类型为 string、directory、boolean、number、integer、select、multi-select，可声明 label/description/required/default/nullable/options；未知类型和关键词不静默忽略。这是冻结设计的字段型表单描述，不能按任意远程 JSON Schema 执行或加载引用。`${PROJECT}`、`${HOME}` 只按当前绑定展开；其它未解析变量拒绝。普通配置不解析 SECRET，占位密钥继续走现有独立 secrets/env 授权与系统凭据库。

插件默认值只填补未设置且未明确删除的字段。成功的 `config_remove` 保持缺失，不回填 schema 默认值；必填约束仍检查。空数组维持空数组，nullable 的 null 保持字面值。

## 授权、进程与证据

- 规范 JSON 最大 16 KiB，只通过该进程的 `AIDOCK_PLUGIN_CONFIG` 交付；不进入原生工具文件、启动参数或普通会话审计正文。禁止父进程同名环境变量和凭据 env 映射覆盖此合同。
- 配置摘要进入新的可选授权 `settings_sha256` 和会话 `configuration_sha256`，空字段保持旧记录字节格式；实际字节必须匹配当前精确授权，不接受原始进程参数或任意环境变量。
- 普通握手、客户端启动、固定已应用视图、冻结候选与回放分别重新编译该精确配置；合并值、清单、安装、产物、运行时或独立授权变化仍使旧请求失效。保存其它草稿不替换固定应用配置。
- 配置交付记录仅由当前精确输入、有效期、成功握手或完成读取的干净客户端会话推导；只证明配置在启动时交付，不证明插件业务或模型采用。失败、过期和不同配置不能冒充当前证据。

## 界面与验收

Profile 的“插件配置”按核验字段生成控件，支持共享/本机、继承/默认、空数组和显式 null；保留其它插件配置，改变字段只作本页草稿，预览后另行确认。显示已保存有效值及来源，解释共享值被本机遮盖；已保存版本的交付记录与未保存草稿区分。删除声明单独显示，明确协调后才编辑，未知值不自动清理。

证据目录 `.artifacts/experience-plugin-configuration-v079/verification.json`。真实双 MCP 与两工具联合应用、分层配置逐进程交付、保存草稿后固定启动、统一快照回放、值不入安全审计通过。最终构建再次通过实际客户端，明确删除字段确实不出现在子进程配置；CLI/API 有效配置与交付记录一致。

内置浏览器完成本机空数组及共享值预览/取消、共享保存和源回退，取消无写入。回退后项目完整树、本机覆盖树及插件产物树逐项 SHA256 与原基线相同，两个实际已应用客户端仍完成 Public 查询、越界拒绝和 EOF 退出。保存草稿不会冒充已应用配置。验收发现本机预览切回共享后确认说明未刷新，已修复并以最终构建复验。1280/390 宽无横向溢出，控制台 warn/error 为零。

十个受影响包完整竞态通过；随后显式删除及交付记录修订通过六包 Profile/Configuration/Replay 定向竞态和 pluginconfig/profile 完整竞态。最终前端 38 个语法、39 组状态测试、Demo 58 组、vet、Windows/Linux 构建通过；Linux 未运行。早期验收脚本的旧二进制路径、授权前后状态比较及旧命令路径断言已纠正，修正日志保留；这些是夹具修正，不计为产品缺陷。

完整 Profile 正式副本同步、通用插件类型/第三方配置适配、插件版本并存/依赖恢复、实际技能 L3/L4、Windows 网络隔离/broker、外部生成器、审计分段及阶段 2–5 继续。原有 owned 技能/规则正式发布范围保持明确，普通配置不能借此自动传播。全部开发验收使用专用 `.artifacts` 项目，真实 E:\work、work2–5 未访问。

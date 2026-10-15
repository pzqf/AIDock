# 系统凭据库与密钥管理

版本：v0.52.0-dev。落实冻结设计 §9.6 的本机 OS 凭据存储与管理入口。阶段 1、运行时注入和完整 Goal 尚未完成。

## 使用

侧栏打开“密钥管理”，输入名称和值一次，核对后确认添加。值默认遮蔽，可显示本次输入。选择已有名称可轮换或删除；旧值不预填、不提供明文读取/导出。成功后、关闭弹窗或页面离开时清空输入，失败保留输入供修正。桌面双栏同时展示已有名称和操作表单，缺失声明按名称折叠，引用使用项目入口名称与作用域。

CLI 使用 secret list、secret add <name> --stdin --confirm、secret rotate <name> --stdin --confirm --expected-revision <revision>。stdin 精确保留字节，包含换行，不接受值参数。删除需要 --confirm --references-reviewed --expected-revision 及 --expected-references，最后一项来自相同绑定下 list 的 review。secret reconcile --confirm 核对未记下结果的操作。

API：GET /api/credentials/state、POST /api/credentials/change、POST /api/credentials/reconcile；沿用会话、Host/Origin 与 CSRF 保护。写入请求拒绝重复键、大小写别名、未知字段、null 和错误类型，诊断不反射请求值。核对接口只接受空 JSON 对象。

## 存储和一致性

Windows 通过 Advapi32 Generic Credentials 存储，作用于当前系统用户。名称空间绑定规范化 Local Root；显式 --home 的 CLI 操作固定使用该 home/local，不能被用户设置重定向。其它平台明确不可用，不降级写文件。名称为小写字母开头的字母、数字、下划线，最多 64 字符；值非空、有效 UTF-8、不含 NUL，最多 2400 字节。

新增重名报错，轮换/删除绑定 OS 库中独立随机修订，拒绝过期确认。跨进程、跨 Windows 登录会话使用当前用户 SID 限定的 Global 命名 mutex，取得与释放期间固定 OS 线程。没有访问其它名称空间或用户凭据。OS 返回的临时字节缓冲用后清理；Go 字符串和浏览器内存不承诺可证明的物理擦除。

磁盘仅记录名称、随机修订、动作、时间和结果，不含值、值长度或值摘要。写系统库前追加并刷盘 INTENT，成功后追加 APPLIED。中断留下未结意图，阻断后续变更。reconcile 只观察当前修订：匹配操作后修订记 APPLIED，匹配操作前修订记 NOT_APPLIED，第三种状态报 RECOVERY_CONFLICT；不重试写入、不恢复旧值。损坏或部分写入的审计记录保留并阻断，当前不自动修复。

审计目前为本机追加哈希链，最多 10000 条，尚无独立可信锚点或归档；不能抵御同用户同时改写 OS 元数据和整条审计链，也不声称两个存储在断电时原子提交。普通项目源快照、回退、同步不涉及这些 OS 值，不能从恢复包找回轮换/删除前的密钥。

## 引用与声明

补齐冻结 §9.6 已约定的可选 secrets 名称数组：mcp.yaml 每个服务器和 plugin.yaml 均支持，最多 64 项、唯一且只允许名称；没有值映射字段。保持 schema_version: 1，旧文件省略时按空声明读取，属于已冻结合同的实现补齐；不是给旧字段更换语义。插件清单与项目定义的名称集合必须一致，否则清单检查失败，顺序无关。

引用审查读取当前显式绑定，以及当前 Local Root 登记的活动、归档和解除登记目录中的 MCP 定义，包含停用服务器。显示缺失名称与无法读取/无效的声明。删除确认绑定所读配置字节、已知引用及未知范围的摘要，变化后需刷新。读取范围不等于发现所有外部客户端、未登记项目或其它数据根；读取与删除之间不持有所有项目源锁，因此它是影响审查，不能替代未来每次启动时的缺失/授权检查。

## 验收

证据：.artifacts/experience-credentials-v052/。六个相关包完整关闭缓存竞态 177 PASS / 0 SKIP / 0 FAIL；源 MCP 定向竞态 82 PASS / 1 SKIP / 0 FAIL，跳过的是仅供子进程调用的 TestMCPArtifactExitHelper，不计为通过。最终对内部错误类型/重复声明加固后，manifest 包再次通过竞态测试。25 组前端状态回归通过。Windows 系统凭据实际读写、独立名称空间、随机修订、并发新增、操作前后中断、删除中断、外部冲突、审计破坏与严格输入有测试。

全新项目 A/B 完成 14 项 CLI/API 端到端验收：stdin 添加、重名拒绝、轮换、过期阻断、两项目缺失/存在状态、独立数据根隔离、删除引用确认、API 严格请求及无回显。两个项目全树与验收前完全一致；递归扫描包含隐藏文件，本机数据根没有测试密钥值。浏览器完成新增、轮换、删除、中文引用影响、成功清空及桌面布局核验，专用测试值已全部删除。go vet、gofmt、diff、Windows 构建及 Linux 交叉构建通过；Linux 未运行，窄屏及静态 Demo 58 用例未重跑。

## 剩余

运行时 ${SECRET:NAME} 展开、声明与授权绑定的按需注入、注入审计、锁定运行时生命周期、OS 隔离/出口 broker、真实项目 MCP 执行及原生字段写入/恢复继续开发。当前 mcp exec 仍预留，execution_ready 保持 false。未访问或修改 E:/work、work2–5。

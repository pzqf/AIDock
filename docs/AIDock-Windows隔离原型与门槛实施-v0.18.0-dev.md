# Windows 隔离原型与执行门槛 · v0.18.0-dev

需求基线保持 v0.24。本轮为阶段 1D 外部生成器接入所需的 OS 前置原型；不声明 1C、1D、1E 或阶段 1 完成。没有执行第三方生成器、技能代码或 AI CLI，没有扫描或修改 `E:/work`、`E:/work2–5`。

## 可使用的入口

工具管理页增加「执行隔离检查」：读取平台能力、显式验证、读取上次记录和逐项详情。打开验证窗口或取消都不启动进程。结果分为目录与完整包、网络边界、进程与清理；UNKNOWN 与历史结果明确标注。原有技能草稿、原生安装/回退、副本候选检查不被替代。

```powershell
# 不启动进程，也不创建 Local 或记录
aidock platform check --home <专用体验数据根> --json

# 只运行当前 AIDock 内置固定测试程序；不能传入 exe/command/root/network
aidock platform check --probe --home <专用体验数据根> --json

# 只读核对最新或指定记录，不重新启动程序
aidock platform check --last --home <专用体验数据根> --json
aidock platform show --check <检查 UUID> --home <专用体验数据根> --json

# 仅读未清理的固定测试；显式清理协议标记的目录和临时 profile
aidock platform pending --home <专用体验数据根> --json
aidock platform recover --home <专用体验数据根> --json
```

API 为 `POST /api/platform/check {probe}`、`GET /api/platform/check/latest`、`POST /api/platform/check/show {check_id}`，以及 `GET /api/platform/check/pending`、`POST /api/platform/check/recover {}`，沿用回环 Host/Origin、会话与 CSRF、防重复请求、关闭服务保护和严格 JSON 字段校验。CLI 和界面共用同一 Manager。静态或不完整证据退出 3；明确阻断退出 5；仅固定原型全部通过才退出 0，仍不授权外部执行。清理另有具体路径确认窗口；取消不执行清理。

## 固定运行合同

- 程序为当前 AIDock 可执行文件的字节副本，启动前、结束后与平台记录均核对 SHA256。隐藏测试入口必须先验证实际 AppContainer 令牌，普通调用在读文件前拒绝。公开 API 不接收外部命令、用户脚本、任意参数或放宽权限。
- 独立夹具只在当前 Local 的 `isolation-probes/probe-*` 下新建。临时 AppContainer SID 仅授予新夹具的通行、输入/执行依赖只读、输出/临时目录可写 ACL，设置 Low mandatory label；不扩大 Local 或项目祖先目录权限。
- 以扩展启动属性附带 AppContainer 的零网络能力和禁止子进程策略；主线程挂起，进入禁止 breakaway、限制进程数量/内存、关闭即终止的 Job 后，宿主核对令牌再恢复线程。使用 detached console 启动，不显示额外终端窗口。
- 环境只有 Windows 初始化所需路径、暂存 TEMP/TMP 和 GOMAXPROCS，继承句柄关闭；不复制用户环境或凭据。Windows 的 APPDATA/LOCALAPPDATA/USERPROFILE 是必要路径变量，不意味着授予相应目录访问。
- 测试含正文、二进制附件和空目录生成；只读输入/依赖修改拒绝；专用项目/私有哨兵和故意设置的 junction 越界读写拒绝；TCP/UDP 直接套接字；禁止子进程；正常/超时进程退出。
- 宿主再次核对输入和哨兵未变、完整输出包、执行摘要、无子进程标记和输出无额外 reparse point。超时进程须启动后再被终止并等到退出，不能把“没启动”算清理通过。
- 结束先终止并等待进程，再移除测试 junction、仅递归删除本次新建夹具、删除临时 profile。清理失败阻断；取消会清理资源，但不保存可复用成功记录。不支持的平台显示 UNAVAILABLE，没有非隔离执行替代。
- 创建 profile 前写入并刷盘专用恢复标记，绑定 UUID、精确根、profile 名与本机身份。验证与恢复共用实际目录旁的 OS 锁，跨进程仍在运行时拒绝清理。父进程突然退出时 Job 随句柄关闭终止子进程，新进程可读残留标记再显式恢复；未清理前阻断新验证。
- 恢复只接受直接子目录 `probe-<UUID>` 与对应 `AIDock.Probe.<UUID>` 标记；根逃逸、损坏标记、额外顶层内容、意外链接均保留并返回冲突。清理按 profile→已知 junction→测试内容→标记→根的顺序，文件占用失败时保留标记供重试；重复恢复幂等。未识别目录绝不自动接管。

## 证据及限制

本机为 Windows 10.0.26300 / amd64。实际 AppContainer 固定测试中，文件/包、目录/junction 越界、子进程禁止与正常/超时清理通过。外出 TCP 返回 WSAEACCES，记录 PASS；回环 TCP 只出现超时，UDP 发送未返回拒绝，记录 UNKNOWN。**超时、无响应、UDP send 成功不能证明 OS 阻断，也不能直接断定泄漏。** 若 TCP 实际连接成功则记录 FAIL。当前整份报告为 UNKNOWN，`generator_ready=false`。

固定报告共 24 项：本机 22 PASS、2 UNKNOWN。真实中断测试在测试子进程恢复 AppContainer 主线程且测试程序写入启动标记后，让父进程 `os.Exit(92)`，由新 Manager 在实际目录锁下清理；中断期间无成功证据记录。另验证取消、文件占用保留恢复标记、额外内容保护、活动锁阻断、再次清理和幂等。

本原型不保证系统公开资源及 AppContainer 自身存储不可访问，不将 Job、工作目录、环境白名单或零网络能力声明单独当作完整沙箱。第三方运行时/DLL/依赖兼容、全网络强制与证据、出口 broker、MCP 会话、外部生成器暂存/校验/完整恢复映射仍待适配。没有借原型开放第三方执行或跨副本发布，既有发布门槛保持关闭。

显式记录在 `Local/platform-checks/<UUID>.json`。摘要覆盖不可变观测，主机身份使用摘要；主机/OS 构建/架构/策略/程序字节变化或超过 24 小时显示 STALE，历史原结论保留。不将诊断记录写为原生安装 qualification。

## 验证

Go 测试覆盖静态零进程/零写入、实际 AppContainer 完整包和越界用例、超时/取消/进程中断清理、直接入口拒绝、取消不保存、恢复标记与冲突、跨进程目录锁、记录篡改/过期/平台/主机/程序/策略变化及 UNKNOWN/FAIL 分类。CLI/API 覆盖显式 home、严格参数、路径逃逸、会话/Origin/CSRF/方法和默认静态。

前端状态回归覆盖显式验证/取消、忙碌/恢复阻断、外部执行按钮永久关闭、网络不足、历史过期、空历史、残留检查/取消/清理和迟到响应。浏览器验收及最终构建结果见本轮开发交付；测试通过不代表 UNKNOWN 网络行已经取得拒绝证据。

最终浏览器验收使用独立五副本体验项目：静态读取与取消均未创建探测/记录目录；真实验证得到 22 PASS / 2 UNKNOWN；真实测试父进程中断后，在界面完成残留检查→清理预览→取消保留→确认恢复。正文未保存草稿仍保留；更新程序后的旧记录正确显示 STALE，重新验证取得当前记录。480 px 页面与结果网格无水平溢出，恢复默认视口，浏览器 warning/error 为空。

专用五副本原源及共同基线的 77 个文件 SHA256、98 个目录未变；最终记录 `dea5f01e-1f37-46a1-b664-add82dc848d3` 为 UNKNOWN / cleanup=true / generator_ready=false，临时探测目录为空，临时 AppContainer profile 无残留。CLI 读取该记录退出 3，未将不完整证据当成功或安装授权。Windows 构建和 Linux 交叉构建分别验证；Linux 运行验收未执行。

最终 `GOMAXPROCS=2 go test -p 1 -race -count=1 ./...`、`go vet -p 1 ./...`、Windows 构建、Linux 程序与 isolation 测试交叉编译通过；9 组前端状态回归和 Demo 的 58 项替代 DOM/timer 回归通过。gofmt、Git whitespace 及 46 个本地文档链接检查通过。真实浏览器证据与这些回归结果独立记录，不把 Mock 或测试通过转换为平台全能力通过。

package mcpsession

// Outcome explains fixed public codes without including process diagnostics.
func Outcome(code string) string {
	switch code {
	case "MCP_CLIENT_CLOSED":
		return "客户端已断开，会话进程已清理；读取转发、返回与拒绝数量分别记录，关闭不代表全部调用成功"
	case "MCP_CLIENT_REVOKED":
		return "当前授权、配置、运行时证据或完整产物已失效；中继已停止，进程已清理，请重新审查后生成客户端配置"
	case "MCP_CLIENT_CANCELED":
		return "客户端会话已取消，进程已清理；不会重放未完成调用"
	case "MCP_CLIENT_FAILED":
		return "客户端会话因协议、传输或上游错误结束，进程已清理；请检查本机会话审计"
	case "MCP_SESSION_MATCH":
		return "锁定插件已完成初始化与可信工具清单核对，进程已清理；未调用业务工具"
	case "MCP_SESSION_TOOL_MISMATCH":
		return "插件返回的工具清单缺少已审查工具。请核对插件实现和工具策略，更新后重新预览安装锁与授权；本次进程已清理"
	case "MCP_SESSION_TIMEOUT":
		return "插件在限定时间内没有完成握手，已清理进程；请检查插件的 stdio 协议实现"
	case "MCP_SESSION_CANCELED":
		return "本次会话检查已取消，已清理进程；不会自动重试"
	case "MCP_SESSION_ARTIFACT_CHANGED":
		return "会话期间源、有效配置、授权或完整产物发生变化；本次结果无效，请重新核对输入"
	case "MCP_SESSION_START_FAILED":
		return "插件未能启动；请核对锁定入口、运行时与已授权密钥是否可用"
	case "MCP_SESSION_AUDIT_FAILED":
		return "会话审计写入失败，协议转发已停止；请检查会话审计与恢复状态"
	case "MCP_SESSION_CLEANUP_FAILED":
		return "未能确认插件或其后代进程完全退出；本次不记为通过，请核对本机进程"
	default:
		return "插件未能完成有效的 stdio 握手；请检查插件协议、可信工具描述及会话审计"
	}
}

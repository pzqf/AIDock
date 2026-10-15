package pluginpermission

import (
	"regexp"

	"github.com/pzqf/AIDock/internal/result"
)

var exactRuntime = regexp.MustCompile(`^(v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?|[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)

func runtimeVersion(s string) bool { return len(s) <= 128 && exactRuntime.MatchString(s) }

func ownTrusted(r Record) bool {
	return r.OwnTrusted && r.Input.Tier == 0 && r.Input.Unisolated != nil && r.Input.Unisolated.SourceType == "local"
}

func confirmUnisolated(input Input, req Edit) error {
	if input.Tier != 0 {
		return nil
	}
	if input.Unisolated == nil || input.Unisolated.SourceType != "local" {
		return result.Fail("AUTHORIZATION_UNISOLATED_LOCAL_REQUIRED", "Tier 0 只对明确选择的自有可信本地插件开放；社区来源需要对应隔离能力", 4)
	}
	if !req.ConfirmUnisolated {
		return result.Fail("AUTHORIZATION_UNISOLATED_CONFIRMATION_REQUIRED", "须单独确认这是自有可信本地插件及锁定运行时，并接受非隔离执行的文件、网络和子进程边界", 4)
	}
	return nil
}

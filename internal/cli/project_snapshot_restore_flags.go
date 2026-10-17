package cli

import (
	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func replayRestoreFlags(cmd *cobra.Command) func() (*native.SnapshotReplayRestoreTarget, error) {
	var plan, digest, binding string
	cmd.Flags().StringVar(&plan, "restore-plan", "", "可选：恢复原应用的共同回退提案 UUID")
	cmd.Flags().StringVar(&digest, "restore-digest", "", "恢复提案完整摘要，与另外两项共同指定")
	cmd.Flags().StringVar(&binding, "applied-binding", "", "待恢复的原应用绑定")
	return func() (*native.SnapshotReplayRestoreTarget, error) {
		if plan == "" && digest == "" && binding == "" {
			return nil, nil
		}
		if plan == "" || digest == "" || binding == "" {
			return nil, result.Fail("INVALID_INPUT", "恢复审查必须共同指定原提案、摘要和应用绑定", 2)
		}
		return &native.SnapshotReplayRestoreTarget{Plan: plan, Digest: digest, Binding: binding}, nil
	}
}

package engine

import (
	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
)

// SnapshotReader is exclusively for archive list/check/assessment operations.
// A damaged current Local profile must not make its recovery materials
// inaccessible. Write entrypoints continue to require Native/Sources instead.
func (e *Engine) SnapshotReader() (*native.Manager, error) {
	return e.snapshotBinding()
}

// AppliedRuntime accepts no caller-supplied frozen config. The native owner
// must prove a committed archive and an actually installed managed field.
func (e *Engine) AppliedRuntime() (*native.Manager, error) { return e.snapshotBinding() }

// SnapshotDraftRecovery is an explicit two-root source-only recovery path.
// The draft compiler checks current identity and all exact target bytes; it
// cannot apply native files or restore authorization through this entrypoint.
func (e *Engine) SnapshotDraftRecovery() (*native.Manager, error) {
	return e.snapshotBinding()
}

// This explicit entry point can change only the current permission authority.
// Archive reconstruction independently validates current identity and drafts;
// no caller-supplied profile, root or historical approval is accepted.
func (e *Engine) SnapshotReplayPermissions() (*native.Manager, error) { return e.snapshotBinding() }

func (e *Engine) snapshotBinding() (*native.Manager, error) {
	if e.SourceRoot == "" {
		return nil, result.Fail("SOURCE_BINDING_REQUIRED", "快照读取需要明确项目源、项目、作用域和副本绑定", 4)
	}
	if e.GlobalRoot != "" {
		return nil, result.Fail("INVALID_SOURCE_BINDING", "项目快照不能使用全局管理绑定", 2)
	}
	m, err := source.New(e.SourceRoot, e.Roots.Local, e.SourceOwner, e.SourceScope)
	if err != nil {
		return nil, err
	}
	if err = m.BindBridgeObservation(e.BridgeFile, e.SourceCopy); err != nil {
		return nil, err
	}
	return native.New(m, e.NativeRoot)
}

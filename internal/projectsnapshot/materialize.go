package projectsnapshot

import (
	"context"
	"sort"

	"github.com/pzqf/AIDock/internal/privatefs"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// Materialize copies complete, checked source/configuration/package bytes back
// to their bound immutable Local cache. It never replaces a live source,
// local-profile root, native target or authority record. Replay still needs its
// own current-policy checks, compilation and coordinated transaction.
func (a *Archive) Materialize(ctx context.Context, id string, destination *workstore.Store) (Record, error) {
	var out Record
	if destination == nil || destination.Root != a.Binding.SourceRoot || destination.Local != a.Local {
		return out, result.Fail("SNAPSHOT_CACHE_BINDING_MISMATCH", "恢复缓存须使用当前快照绑定的源与 Local Root", 2)
	}
	r, err := a.Load(ctx, id)
	if err != nil {
		return out, err
	}
	entries := map[string]workstore.Entry{}
	for _, tree := range sourceTrees(r.Content) {
		for _, e := range tree.Entries {
			if !e.Dir {
				entries[e.Hash] = e
			}
		}
	}
	keys := make([]string, 0, len(entries))
	for h := range entries {
		keys = append(keys, h)
	}
	sort.Strings(keys)
	err = destination.Locked(func() error {
		for _, h := range keys {
			if err := ctx.Err(); err != nil {
				return err
			}
			e := entries[h]
			body, err := a.SourceData(ctx, r, e)
			if err != nil {
				return err
			}
			scratch := workstore.Tree{Entries: map[string]workstore.Entry{}}
			if err = destination.PutFile(&scratch, h, body, e.Mode); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	return r, nil
}

// Native assets are never restored to a project or ordinary source cache.
func (a *Archive) MaterializeNative(ctx context.Context, id string, destination *workstore.Store) (Record, error) {
	var out Record
	if destination == nil || destination.Root != a.Binding.NativeRoot || destination.Local == a.Local || !workstore.Within(a.Local, destination.Local) {
		return out, result.Fail("SNAPSHOT_CACHE_BINDING_MISMATCH", "原生材料只能进入当前项目的受限本机缓存", 2)
	}
	if err := privatefs.Ensure(destination.Local); err != nil {
		return out, err
	}
	r, err := a.Load(ctx, id)
	if err != nil {
		return out, err
	}
	for _, tree := range nativeTrees(r.Content) {
		for _, e := range tree.Entries {
			if e.Dir {
				continue
			}
			if err := ctx.Err(); err != nil {
				return out, err
			}
			body, err := a.nativeBlobs().Data(e)
			if err != nil {
				return out, err
			}
			scratch := workstore.Tree{Entries: map[string]workstore.Entry{}}
			if err = destination.PutFile(&scratch, e.Hash, body, e.Mode); err != nil {
				return out, err
			}
		}
	}
	return r, nil
}

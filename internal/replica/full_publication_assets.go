package replica

import (
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// RestoreSelected captures later drafts and unrelated native siblings into
// each REAL bound root's private cache. Revision compilation consumes the
// common cache instead. Retain these exact immutable images before crossing
// that boundary; never reread live files or copy raw native bytes to source.
func retainFullRestorationImages(c *workstore.Coordinator, tx workstore.BatchPlan) error {
	if err := c.ValidateManifest(tx); err != nil {
		return err
	}
	stored, err := c.LoadPlan(tx.ID)
	if err != nil {
		return err
	}
	if string(data(stored)) != string(data(tx)) {
		return result.Fail("FULL_PUBLICATION_INVALID", "恢复材料必须来自实际保存的共同提案", 5)
	}
	roots := map[string]workstore.RootBinding{}
	for _, root := range c.Roots {
		roots[root.Path] = root
	}
	for _, target := range tx.Targets {
		root, ok := roots[target.Path]
		if !ok {
			return result.Fail("INVALID_ROOTS", "共同恢复含未绑定的备份根", 5)
		}
		from := &workstore.Store{Root: root.Root, Local: c.Local}
		for _, tree := range []workstore.Tree{target.Before, target.After} {
			for _, entry := range tree.Entries {
				if entry.Dir {
					continue
				}
				body, err := from.Data(entry)
				if err != nil {
					return result.Fail("BACKUP_INVALID", "真实共同恢复缺少某个原件或后续草稿的完整备份", 5)
				}
				scratch := workstore.Tree{Entries: map[string]workstore.Entry{}}
				if err = c.Blobs.PutFile(&scratch, "content", body, entry.Mode); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

package replica

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

func (m *Manager) fullPublicationReferences(binding Binding) ([]Reference, error) {
	out := []Reference{}
	dir := filepath.Join(m.fullPublicationDir(), "previews")
	if err := workstore.CheckLinks(dir); err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if strings.HasPrefix(f.Name(), ".aidock-tmp-") {
			continue
		}
		if f.IsDir() || filepath.Ext(f.Name()) != ".json" {
			return nil, result.Fail("FULL_PUBLICATION_INVALID", "完整批次恢复引用目录含未知记录", 5)
		}
		p, err := m.readFullPublicationDescriptor(strings.TrimSuffix(f.Name(), ".json"), "")
		if err != nil {
			return nil, err
		}
		relevant := p.Proof.Publisher.Copy == binding.ID && p.Proof.Publisher.SourceRoot == binding.Root || p.InitiatorCopy == binding.ID && p.InitiatorRoot == binding.Root
		for _, member := range p.Proof.Members {
			relevant = relevant || member.Copy == binding.ID && member.SourceRoot == binding.Root
		}
		c, _ := m.fullPublicationCoordinator(p)
		js, err := c.History()
		if err != nil {
			return nil, err
		}
		// Corruption in another initiating binding is never silently skipped.
		if !relevant {
			continue
		}
		for _, j := range js {
			if j.State != "committed" && j.State != "prepared" {
				continue
			}
			state := j.State
			pending, err := c.MarkersPending(j.Plan.ID)
			if err != nil {
				return nil, err
			}
			if pending {
				state = "prepared"
			}
			out = append(out, Reference{Kind: "publication", ID: j.Plan.ID, State: state, Reason: "完整批次 " + p.ID + " 的共同恢复/回退仍绑定此成员或发起入口"})
		}
	}
	return out, nil
}

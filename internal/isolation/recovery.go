package isolation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type Pending struct {
	Version int    `json:"schema_version"`
	ID      string `json:"id"`
	Root    string `json:"root"`
	Profile string `json:"profile"`
	Host    string `json:"host_digest"`
	Digest  string `json:"digest"`
}

func pendingDigest(p Pending) string { p.Digest = ""; return workstore.Hash(bytes(p)) }
func writePending(p Pending) error {
	p.Digest = pendingDigest(p)
	f, err := os.OpenFile(filepath.Join(p.Root, ".aidock-isolation.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(bytes(p))
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	return err
}

// Pending is read-only and never deletes or grants permission to any path.
// Only protocol-marked, direct children of our dedicated probe area qualify.
func (m *Manager) Pending() ([]Pending, error) {
	base := filepath.Join(m.Local, "isolation-probes")
	if err := workstore.CheckLinks(base); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return []Pending{}, nil
	}
	if err != nil {
		return nil, err
	}
	platform := platformInfo()
	out := []Pending{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "probe-") {
			continue
		}
		root := filepath.Join(base, entry.Name())
		if err = workstore.CheckLinks(root); err != nil {
			return nil, err
		}
		b, err := regularFile(filepath.Join(root, ".aidock-isolation.json"))
		var p Pending
		if err != nil || json.Unmarshal(b, &p) != nil || p.Version != 1 || !uuid.MatchString(p.ID) || p.Root != root || entry.Name() != "probe-"+p.ID || p.Profile != "AIDock.Probe."+p.ID || p.Host != platform.Host || p.Digest != pendingDigest(p) {
			return nil, result.Fail("ISOLATION_RECOVERY_CONFLICT", "未识别的测试目录或恢复标记损坏；保留原内容，拒绝自动清理 "+root, 6)
		}
		out = append(out, p)
	}
	return out, nil
}
func (m *Manager) Recover() (int, error) {
	count := 0
	err := lockPlatform(m.Local, func() error {
		pending, err := m.Pending()
		if err != nil {
			return err
		}
		for _, p := range pending {
			if err = recoverPlatform(p); err != nil {
				return result.Fail("ISOLATION_RECOVERY_CONFLICT", err.Error(), 6)
			}
			count++
		}
		return nil
	})
	return count, err
}

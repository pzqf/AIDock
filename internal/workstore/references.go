package workstore

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/pzqf/AIDock/internal/result"
)

// NativeHistoryForRoot includes every historical identity for a project,
// including CLI bindings created before copy IDs were canonical. It reads
// atomic journals only; callers hold the source lock when using this as a
// mutation gate. Independent rehearsal roots are excluded by their exact Root.
func NativeHistoryForRoot(local, root string) ([]BatchJournal, error) {
	out := []BatchJournal{}
	dir := filepath.Join(local, "native-transactions")
	if err := CheckLinks(dir); err != nil {
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
		if _, err = hex.DecodeString(f.Name()); err != nil || len(f.Name()) != 64 || !f.IsDir() {
			return nil, result.Fail("BACKUP_INVALID", "工具事务目录含未知记录", 5)
		}
		journals := filepath.Join(dir, f.Name(), "journals")
		if err = CheckLinks(journals); err != nil {
			return nil, err
		}
		entries, e := os.ReadDir(journals)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, e
		}
		seen := map[string]bool{}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".aidock-tmp-") {
				continue
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				return nil, result.Fail("BACKUP_INVALID", "工具事务日志含未知记录", 5)
			}
			bytes, e := ReadFile(filepath.Join(journals, entry.Name()))
			if e != nil {
				return nil, e
			}
			var j BatchJournal
			if e = json.Unmarshal(bytes, &j); e != nil {
				return nil, e
			}
			if j.Plan.Version != 1 || j.Plan.Kind != "native-targets" || !filepath.IsAbs(j.Plan.Root) || j.Plan.Identity == "" || j.Plan.Digest != batchDigest(j.Plan) || entry.Name() != j.Plan.ID+".json" {
				return nil, result.Fail("BACKUP_INVALID", "无法判定损坏工具快照的目录引用，请先修复记录", 5)
			}
			if !strings.EqualFold(j.Plan.Root, root) {
				continue
			}
			paths := []string{}
			for _, target := range j.Plan.Targets {
				paths = append(paths, target.Path)
			}
			b := &Batch{Local: local, Boundary: j.Plan.Root, Identity: j.Plan.Identity, Paths: paths}
			if b.dir() != filepath.Join(dir, f.Name()) || j.Plan.Root != filepath.Clean(j.Plan.Root) {
				return nil, result.Fail("BACKUP_INVALID", "工具事务根与记录目录不一致", 5)
			}
			if seen[b.Identity] {
				continue
			}
			validated, e := b.journals()
			if e != nil {
				return nil, e
			}
			out = append(out, validated...)
			seen[b.Identity] = true
		}
	}
	return out, nil
}

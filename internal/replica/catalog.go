package replica

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

// Catalog reads only canonical replica registries below the explicitly chosen
// Local Root. Project navigation does not maintain a second path database.
func Catalog(local string) ([]Registry, error) {
	root := filepath.Join(local, "replica-control")
	out := []Registry{}
	if err := workstore.CheckLinks(root); err != nil {
		return nil, err
	}
	files, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	hash := regexp.MustCompile(`^[a-f0-9]{64}$`)
	for _, f := range files {
		if !f.IsDir() && strings.HasPrefix(f.Name(), ".") && strings.HasSuffix(f.Name(), ".aidock-coordinated-pending.json") {
			return nil, result.Fail("COORDINATED_RECOVERY_REQUIRED", "项目登记曾中断；请通过项目接入历史恢复", 6)
		}
		if !f.IsDir() && strings.HasPrefix(f.Name(), ".") && strings.HasSuffix(f.Name(), ".aidock-lock") {
			continue
		}
		if !f.IsDir() || !hash.MatchString(f.Name()) {
			return nil, result.Fail("REPLICA_REGISTRY_INVALID", "登记目录含未识别条目", 5)
		}
		path := filepath.Join(root, f.Name())
		if err = workstore.CheckLinks(path); err != nil {
			return nil, err
		}
		if err = workstore.CoordinatedPending(path); err != nil {
			return nil, err
		}
		store := &workstore.Store{Root: path, Local: filepath.Join(local, "replica-journals"), PlanKind: "replica-control"}
		if err = store.Pending(); err != nil {
			return nil, err
		}
		bytes, e := workstore.ReadFile(filepath.Join(path, "replicas.json"))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, e
		}
		var r Registry
		if e = decode(bytes, &r); e != nil {
			return nil, e
		}
		if f.Name() != workstore.Hash([]byte(r.Owner+"/"+r.Scope)) {
			return nil, result.Fail("REPLICA_REGISTRY_INVALID", "登记与目录身份不一致", 5)
		}
		m := &Manager{Source: &source.Manager{Owner: r.Owner, Scope: r.Scope, Store: &workstore.Store{Local: local}}}
		if e = m.validateRegistry(r); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}

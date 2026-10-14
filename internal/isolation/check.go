// Package isolation verifies an OS boundary using only AIDock's fixed probe.
// It does not accept external commands or authorize generator execution.
package isolation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"time"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

const Policy = "windows-appcontainer-probe-v1"
const boundary = "此记录只验收 AIDock 固定测试程序的 Windows AppContainer 原型：声明目录、无网络能力、禁止子进程与 Job 清理。测试使用 Local 下的独立夹具，不访问登记项目，不执行外部生成器或技能。系统公开资源及 AppContainer 自身存储不属本原型的禁止范围；第三方运行时、出口 broker 和 MCP 会话尚未验收，记录不能授权发布或外部执行。"

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Platform struct {
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	Kernel     string `json:"kernel"`
	Host       string `json:"host_digest"`
	APIs       bool   `json:"apis_available"`
	Executable string `json:"executable_sha256"`
	Policy     string `json:"policy"`
}
type Finding struct {
	Code   string `json:"code"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}
type Report struct {
	Version        int       `json:"schema_version"`
	Kind           string    `json:"kind"`
	ID             string    `json:"id,omitempty"`
	Created        string    `json:"created"`
	Expires        string    `json:"expires,omitempty"`
	Platform       Platform  `json:"platform"`
	Probe          bool      `json:"probe"`
	Status         string    `json:"status"`
	Findings       []Finding `json:"findings"`
	GeneratorReady bool      `json:"generator_ready"`
	Cleanup        bool      `json:"cleanup"`
	Boundary       string    `json:"boundary"`
	Digest         string    `json:"digest,omitempty"`
	Stale          bool      `json:"stale"`
	StaleReason    string    `json:"stale_reason,omitempty"`
}
type Manager struct {
	Local string
	// Tests can replace the fixed runner, never the public API.
	run func(context.Context, string, Platform) ([]Finding, bool)
}

func New(local string) (*Manager, error) {
	if !filepath.IsAbs(local) || filepath.Clean(local) != local {
		return nil, result.Fail("INVALID_ROOTS", "隔离检查需要规范绝对 Local 根", 2)
	}
	if err := workstore.CheckLinks(local); err != nil {
		return nil, result.Fail("PATH_UNSAFE", err.Error(), 5)
	}
	return &Manager{Local: local}, nil
}
func bytes(v any) []byte { b, _ := json.Marshal(v); return b }
func digest(r Report) string {
	r.Digest, r.Stale, r.StaleReason = "", false, ""
	return workstore.Hash(bytes(r))
}
func executableBytes() ([]byte, error) {
	path, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if err = workstore.CheckLinks(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (128<<20)+1))
	if len(b) > 128<<20 {
		return nil, fmt.Errorf("检查程序超过 128 MiB")
	}
	return b, err
}
func current() (Platform, error) {
	p := platformInfo()
	p.OS, p.Arch, p.Policy = runtime.GOOS, runtime.GOARCH, Policy
	b, err := executableBytes()
	if err != nil {
		return p, err
	}
	p.Executable = workstore.Hash(b)
	return p, nil
}
func (m *Manager) Check(ctx context.Context, probe bool) (Report, error) {
	p, err := current()
	if err != nil {
		return Report{}, err
	}
	r := Report{Version: 1, Kind: "platform-isolation-check", Created: time.Now().UTC().Format(time.RFC3339Nano), Platform: p, Probe: probe, Status: "UNKNOWN", Findings: []Finding{}, Boundary: boundary}
	if !probe {
		r.Findings = append(r.Findings, Finding{"OS_BOUNDARY", "UNKNOWN", "仅读取平台 API 和当前程序摘要；尚未验证实际目录、网络或进程边界"})
		if !p.APIs {
			r.Status = "UNAVAILABLE"
			r.Findings[0].Status = "UNAVAILABLE"
			r.Findings[0].Detail = "当前平台缺少此 Windows 原型 API；不启动进程，不降级为非隔离执行"
		}
		return r, ctx.Err()
	}
	if err = ctx.Err(); err != nil {
		return r, err
	}
	r.ID, r.Expires = workstore.ID(), time.Now().UTC().Add(24*time.Hour).Format(time.RFC3339Nano)
	if !p.APIs {
		r.Status = "UNAVAILABLE"
		r.Findings = append(r.Findings, Finding{"OS_BOUNDARY", "UNAVAILABLE", "Windows AppContainer 原型不可用；未启动进程，未使用非隔离替代"})
	} else {
		run := m.run
		if run == nil {
			run = probePlatform
		}
		r.Findings, r.Cleanup = run(ctx, m.Local, p)
		r.Status = "PASS"
		if len(r.Findings) == 0 {
			r.Status = "FAIL"
		}
		for _, f := range r.Findings {
			if f.Status == "FAIL" {
				r.Status = "FAIL"
			} else if f.Status != "PASS" && r.Status != "FAIL" {
				r.Status = "UNKNOWN"
			}
		}
		if !r.Cleanup {
			r.Status = "FAIL"
		}
	}
	// Even failure evidence is saved only after the runner has cleaned up.
	// A cancellation never leaves a reusable successful observation.
	if err = ctx.Err(); err != nil {
		if p.APIs && !r.Cleanup {
			return r, result.Fail("ISOLATION_CLEANUP_REQUIRED", "检查已取消，但临时隔离资源清理未完成；查看清理失败信息", 6)
		}
		return r, err
	}
	after, err := current()
	if err != nil {
		return r, err
	}
	if after != p {
		return r, result.Fail("CHECK_STALE", "平台或检查程序在验证期间变化；结果未保存", 3)
	}
	r.Digest = digest(r)
	if err = m.save(r); err != nil {
		return r, err
	}
	return r, nil
}
func (m *Manager) directory() string { return filepath.Join(m.Local, "platform-checks") }
func (m *Manager) save(r Report) error {
	dir := m.directory()
	if err := workstore.CheckLinks(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".record-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	_, err = f.Write(bytes(r))
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(dir, r.ID+".json"))
}
func (m *Manager) load(id string) (Report, error) {
	if !uuid.MatchString(id) {
		return Report{}, result.Fail("INVALID_INPUT", "无效隔离检查 id", 2)
	}
	path := filepath.Join(m.directory(), id+".json")
	if err := workstore.CheckLinks(path); err != nil {
		return Report{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return Report{}, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return Report{}, err
	}
	var r Report
	if len(b) > 1<<20 || json.Unmarshal(b, &r) != nil || r.Version != 1 || r.Kind != "platform-isolation-check" || r.ID != id || !r.Probe || r.Digest != digest(r) || r.GeneratorReady {
		return r, result.Fail("CHECK_RECORD_INVALID", "隔离检查记录无效或摘要不一致", 5)
	}
	return r, nil
}
func (m *Manager) Show(id string) (Report, error) {
	r, err := m.load(id)
	if err != nil {
		return r, err
	}
	p, err := current()
	created, e1 := time.Parse(time.RFC3339Nano, r.Created)
	expires, e2 := time.Parse(time.RFC3339Nano, r.Expires)
	now := time.Now()
	if err != nil || p != r.Platform || e1 != nil || e2 != nil || created.After(now.Add(time.Minute)) || !expires.After(created) || expires.Sub(created) > 24*time.Hour+time.Second || !now.Before(expires) {
		r.Stale, r.StaleReason = true, "平台、主机、检查程序、策略或有效期已变化；历史结果不能作为当前证据"
	}
	return r, nil
}
func (m *Manager) Latest() (*Report, error) {
	if err := workstore.CheckLinks(m.directory()); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(m.directory())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var reports []Report
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".json" {
			continue
		}
		r, err := m.load(name[:len(name)-5])
		if err != nil {
			return nil, err
		}
		reports = append(reports, r)
	}
	if len(reports) == 0 {
		return nil, nil
	}
	sort.Slice(reports, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, reports[i].Created)
		b, _ := time.Parse(time.RFC3339Nano, reports[j].Created)
		if a.Equal(b) {
			return reports[i].ID > reports[j].ID
		}
		return a.After(b)
	})
	r, err := m.Show(reports[0].ID)
	return &r, err
}

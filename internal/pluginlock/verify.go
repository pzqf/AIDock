package pluginlock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/workstore"
)

type Check struct {
	Name             string              `json:"name"`
	Status           string              `json:"status"`
	ArtifactStatus   string              `json:"artifact_status"`
	DependencyStatus string              `json:"dependency_status"`
	TrustLevel       string              `json:"trust_level"`
	OriginSHA256     string              `json:"origin_sha256,omitempty"`
	SecuritySHA256   string              `json:"security_sha256,omitempty"`
	ArtifactSHA256   string              `json:"artifact_sha256,omitempty"`
	ObservedSHA256   string              `json:"observed_sha256,omitempty"`
	Files            int                 `json:"files"`
	Bytes            int64               `json:"bytes"`
	Checks           []inventory.Finding `json:"checks"`
	ExecutionReady   bool                `json:"execution_ready"`
}

func (c *Check) Add(status, code, detail string) {
	c.Checks = append(c.Checks, inventory.Finding{Status: status, Code: code, Detail: detail})
	if status == "FAIL" || c.Status != "FAIL" && status == "UNKNOWN" {
		c.Status = status
	}
}

// Observe reads an exact local tree including hidden files and empty directories.
// It uses a rooted handle, rejects links, checks identities and changes around
// reads, streams large runtime binaries, and never writes the artifact directory.
func Observe(ctx context.Context, root string) ([]Entry, map[string][]byte, error) {
	entries, documents := []Entry{}, map[string][]byte{}
	if err := workstore.CheckLinks(root); err != nil {
		return entries, documents, err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return entries, documents, err
	}
	defer r.Close()
	seen := map[string]bool{}
	total := int64(0)
	documentBytes := int64(0)
	err = fs.WalkDir(r.FS(), ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if err := workstore.SafeRelative(rel); err != nil {
			return err
		}
		if len(entries) >= MaxEntries {
			return fmt.Errorf("产物超过文件数量限制")
		}
		key := strings.ToLower(rel)
		if seen[key] {
			return fmt.Errorf("产物路径大小写冲突: %s", rel)
		}
		seen[key] = true
		if err := workstore.CheckLinks(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			return err
		}
		before, err := r.Lstat(rel)
		if err != nil {
			return err
		}
		entry := Entry{Path: rel, Directory: before.IsDir(), Mode: uint32(before.Mode().Perm())}
		if before.IsDir() {
			entries = append(entries, entry)
			return nil
		}
		if !before.Mode().IsRegular() || before.Size() > MaxFileBytes {
			return fmt.Errorf("非普通文件或超限: %s", rel)
		}
		f, err := r.Open(rel)
		if err != nil {
			return err
		}
		defer f.Close()
		opened, err := f.Stat()
		if err != nil || !os.SameFile(before, opened) {
			return fmt.Errorf("产物在打开时变化: %s", rel)
		}
		h := sha256.New()
		buf := make([]byte, 128<<10)
		var doc []byte
		capture := rel == "plugin.yaml" || filepath.Base(rel) == "package.json" || filepath.Base(rel) == "package-lock.json"
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, readErr := f.Read(buf)
			entry.Bytes += int64(n)
			total += int64(n)
			if entry.Bytes > MaxFileBytes || total > MaxArtifactBytes {
				return fmt.Errorf("产物超出字节限制")
			}
			if n > 0 {
				h.Write(buf[:n])
				if capture {
					if rel == "plugin.yaml" && len(doc)+n > 1<<20 {
						return fmt.Errorf("plugin.yaml 超过 1 MB")
					}
					if len(doc)+n > 8<<20 {
						return fmt.Errorf("依赖文档超过 8 MB")
					}
					doc = append(doc, buf[:n]...)
					documentBytes += int64(n)
					if documentBytes > 32<<20 {
						return fmt.Errorf("依赖文档合计超过 32 MB")
					}
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return readErr
			}
		}
		after, err := f.Stat()
		if err != nil || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
			return fmt.Errorf("产物在读取时变化: %s", rel)
		}
		last, err := r.Lstat(rel)
		if err != nil || !os.SameFile(opened, last) || last.Mode() != after.Mode() || last.Size() != after.Size() || !last.ModTime().Equal(after.ModTime()) {
			return fmt.Errorf("产物在读取后变化: %s", rel)
		}
		if err := workstore.CheckLinks(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			return err
		}
		entry.SHA256 = hex.EncodeToString(h.Sum(nil))
		entries = append(entries, entry)
		if capture {
			documents[rel] = doc
		}
		return f.Close()
	})
	if err == nil {
		err = workstore.CheckLinks(root)
	}
	return entries, documents, err
}

// Observation is returned from the same rooted read used to verify the lock.
// Consumers must require ArtifactStatus PASS before interpreting its documents.
type Observation struct {
	Entries   []Entry
	Documents map[string][]byte
}

func Verify(ctx context.Context, local string, p Plugin, server map[string]any) (Check, error) {
	c, _, err := VerifyObserved(ctx, local, p, server)
	return c, err
}

func VerifyObserved(ctx context.Context, local string, p Plugin, server map[string]any) (Check, Observation, error) {
	var observation Observation
	c := Check{Name: p.Name, Status: "PASS", ArtifactStatus: "UNKNOWN", DependencyStatus: "UNKNOWN", TrustLevel: "L0", ArtifactSHA256: p.Artifact.SHA256, Checks: []inventory.Finding{}}
	if err := ctx.Err(); err != nil {
		return c, observation, err
	}
	if server == nil || Definition(server) != p.DefinitionSHA256 || string(bytesOf(server["source"])) != string(bytesOf(p.Source)) || server["entrypoint"] != p.Entrypoint {
		c.Add("FAIL", "MCP_LOCK_DEFINITION_MISMATCH", "锁定的来源、入口、权限或隔离声明与当前 MCP 定义不一致；需重新评估并生成锁")
	}
	if p.Platform != runtime.GOOS+"-"+runtime.GOARCH {
		c.Add("FAIL", "MCP_LOCK_PLATFORM_MISMATCH", "产物平台 "+p.Platform+" 与当前平台 "+runtime.GOOS+"-"+runtime.GOARCH+" 不符")
	}
	root := filepath.Join(local, filepath.FromSlash(p.Artifact.Path))
	entries, documents, err := Observe(ctx, root)
	observation = Observation{Entries: entries, Documents: documents}
	c.ObservedSHA256 = Digest(entries)
	if ctx.Err() != nil {
		return c, observation, ctx.Err()
	}
	if err != nil {
		c.ArtifactStatus = "FAIL"
		c.Add("FAIL", "MCP_ARTIFACT_UNAVAILABLE", "无法安全核验本地产物: "+err.Error())
	} else {
		want := map[string]Entry{}
		got := map[string]Entry{}
		for _, e := range p.Artifact.Entries {
			want[e.Path] = e
		}
		for _, e := range entries {
			got[e.Path] = e
			if !e.Directory {
				c.Files++
				c.Bytes += e.Bytes
			}
		}
		mismatches := 0
		for _, e := range p.Artifact.Entries {
			if actual, ok := got[e.Path]; !ok || actual != e {
				mismatches++
				if mismatches <= 30 {
					c.Add("FAIL", "MCP_ARTIFACT_CHANGED", "文件/目录缺失或字节、权限变化: "+e.Path)
				}
			}
		}
		for _, e := range entries {
			if _, ok := want[e.Path]; !ok {
				mismatches++
				if mismatches <= 30 {
					c.Add("FAIL", "MCP_ARTIFACT_EXTRA", "存在未锁定的文件或目录: "+e.Path)
				}
			}
		}
		if mismatches > 30 {
			c.Add("FAIL", "MCP_ARTIFACT_MORE_CHANGES", fmt.Sprintf("共有 %d 项差异，仅显示前 30 项", mismatches))
		}
		if mismatches == 0 {
			c.ArtifactStatus = "PASS"
			c.Add("PASS", "MCP_ARTIFACT_MATCH", fmt.Sprintf("完整产物匹配：%d 个文件，%d 字节；入口与运行时均在清单内", c.Files, c.Bytes))
		} else {
			c.ArtifactStatus = "FAIL"
		}
		if p.Resolution.Format == "npm-package-lock-v3" {
			if err = checkNPM(p, documents); err != nil {
				c.DependencyStatus = "FAIL"
				c.Add("FAIL", "MCP_DEPENDENCY_MISMATCH", err.Error())
			} else {
				c.DependencyStatus = "PASS"
				c.Add("PASS", "MCP_DEPENDENCIES_MATCH", "npm v3 完整解析锁、依赖清单与本地 package.json 一致")
			}
		} else {
			c.Add("UNKNOWN", "MCP_DEPENDENCIES_DECLARED", "依赖仅有声明；尚无支持的完整解析锁可核验")
		}
		if !strings.HasPrefix(p.Platform, "windows-") {
			if e := got[p.Runtime.Executable]; e.Mode&0111 == 0 {
				c.Add("FAIL", "MCP_RUNTIME_NOT_EXECUTABLE", "锁定的本地运行时没有执行位")
			}
		}
	}
	c.Add("UNKNOWN", "MCP_SOURCE_TRUST_UNVERIFIED", "L0：当前仅核对锁声明与本地字节，未验证发布者、上游摘要或安全评估；自算哈希不提升来源信任")
	c.Add("UNKNOWN", "MCP_RUNTIME_VERSION_UNVERIFIED", "运行时文件已纳入字节锁；版本为声明，尚无可信发行证据，核验过程不执行 --version")
	return c, observation, nil
}

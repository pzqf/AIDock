// Package pluginorigin verifies offline artifact-origin statements against
// explicitly enrolled project keys. A valid origin signature is not a safety
// assessment, project execution grant, or proof of OS isolation.
package pluginorigin

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/workstore"
)

type Key struct {
	ID         string `json:"id,omitempty"`
	Label      string `json:"label"`
	PublicKey  string `json:"public_key"`
	SourceType string `json:"source_type"`
	SourceID   string `json:"source_id"`
	ExpiresAt  string `json:"expires_at"`
	Enabled    bool   `json:"enabled"`
}
type Keys struct {
	Version int   `json:"schema_version"`
	Keys    []Key `json:"keys"`
}
type Payload struct {
	Version                int    `json:"schema_version"`
	Kind                   string `json:"kind"`
	SubjectSHA256          string `json:"subject_sha256"`
	Publisher              string `json:"publisher"`
	ReleaseURL             string `json:"release_url"`
	IssuedAt               string `json:"issued_at"`
	ExpiresAt              string `json:"expires_at"`
	RuntimeVersionVerified bool   `json:"runtime_version_verified"`
}
type Envelope struct {
	Version   int     `json:"schema_version"`
	KeyID     string  `json:"key_id"`
	Payload   Payload `json:"payload"`
	Signature string  `json:"signature"`
}
type Subject struct {
	Name           string `json:"name"`
	SHA256         string `json:"sha256"`
	SourceType     string `json:"source_type"`
	SourceID       string `json:"source_id"`
	Version        string `json:"version"`
	Platform       string `json:"platform"`
	ArtifactSHA256 string `json:"artifact_sha256"`
}
type Check struct {
	Name                   string              `json:"name"`
	SubjectSHA256          string              `json:"subject_sha256"`
	Status                 string              `json:"status"`
	KeyID                  string              `json:"key_id,omitempty"`
	KeyLabel               string              `json:"key_label,omitempty"`
	Publisher              string              `json:"publisher,omitempty"`
	ExpiresAt              string              `json:"expires_at,omitempty"`
	RuntimeVersionVerified bool                `json:"runtime_version_verified"`
	Checks                 []inventory.Finding `json:"checks"`
}

func bytesOf(v any) []byte { b, _ := json.Marshal(v); return b }
func JSON(v any) []byte    { b, _ := json.MarshalIndent(v, "", "  "); return append(b, '\n') }
func digest(s string) bool { return len(s) == 64 && strings.Trim(s, "0123456789abcdef") == "" }
func Fingerprint(publicKey string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil || len(b) != ed25519.PublicKeySize || base64.StdEncoding.EncodeToString(b) != publicKey {
		return "", fmt.Errorf("Ed25519 公钥须为规范 Base64 编码的 32 字节")
	}
	return workstore.Hash(b), nil
}
func Source(p pluginlock.Plugin) (string, string) {
	kind, _ := p.Source["type"].(string)
	field := "package"
	if kind == "github" {
		field = "url"
	}
	if kind == "local" {
		field = "path"
	}
	id, _ := p.Source[field].(string)
	return kind, id
}

// Subject omits the project alias, local installation path and permission
// choices. They are not publisher claims. Definition/authorization checks still
// bind those independently; every actual artifact and runtime byte is signed.
func For(p pluginlock.Plugin) Subject {
	kind, id := Source(p)
	deps := append([]pluginlock.Dependency{}, p.Dependencies...)
	sort.Slice(deps, func(i, j int) bool { return deps[i].Path < deps[j].Path })
	runtimeHash := ""
	for _, e := range p.Artifact.Entries {
		if e.Path == p.Runtime.Executable {
			runtimeHash = e.SHA256
		}
	}
	bound := map[string]any{"schema_version": 1, "source_type": kind, "source_id": id, "version": p.Version, "platform": p.Platform, "entrypoint": p.Entrypoint, "artifact_sha256": p.Artifact.SHA256, "runtime": p.Runtime, "runtime_sha256": runtimeHash, "resolution": p.Resolution, "dependencies": deps}
	return Subject{Name: p.Name, SHA256: workstore.Hash(bytesOf(bound)), SourceType: kind, SourceID: id, Version: p.Version, Platform: p.Platform, ArtifactSHA256: p.Artifact.SHA256}
}
func ParseKeys(b []byte) (Keys, error) {
	var out Keys
	v, err := config.Validate("plugin-origin-keys", b)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(bytesOf(v), &out); err != nil {
		return out, err
	}
	seen := map[string]bool{}
	for i, k := range out.Keys {
		id, err := Fingerprint(k.PublicKey)
		if err != nil {
			return out, err
		}
		if k.ID != "" && k.ID != id {
			return out, fmt.Errorf("公钥指纹不匹配")
		}
		if seen[id] {
			return out, fmt.Errorf("公钥重复；每个公钥只允许一个明确来源范围")
		}
		seen[id] = true
		out.Keys[i].ID = id
		if strings.TrimSpace(k.Label) == "" || strings.TrimSpace(k.SourceID) == "" || strings.ContainsAny(k.SourceID, "\r\n\x00") {
			return out, fmt.Errorf("密钥名称或来源范围无效")
		}
		if _, err = time.Parse(time.RFC3339, k.ExpiresAt); err != nil {
			return out, fmt.Errorf("密钥有效期须为 RFC3339 时间")
		}
	}
	return out, nil
}
func ParseEnvelope(b []byte) (Envelope, error) {
	var out Envelope
	v, err := config.Validate("plugin-origin-evidence", b)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(bytesOf(v), &out); err != nil {
		return out, err
	}
	sig, err := base64.StdEncoding.DecodeString(out.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize || base64.StdEncoding.EncodeToString(sig) != out.Signature {
		return out, fmt.Errorf("签名须为规范 Base64 编码的 Ed25519 签名")
	}
	issued, err := time.Parse(time.RFC3339, out.Payload.IssuedAt)
	if err != nil {
		return out, fmt.Errorf("签发时间无效")
	}
	expires, err := time.Parse(time.RFC3339, out.Payload.ExpiresAt)
	if err != nil || !expires.After(issued) || expires.Sub(issued) > 90*24*time.Hour {
		return out, fmt.Errorf("证据有效期须晚于签发时间且最长 90 天")
	}
	u, err := url.Parse(out.Payload.ReleaseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return out, fmt.Errorf("发行链接须为不含凭据或查询参数的 HTTPS URL")
	}
	if strings.TrimSpace(out.Payload.Publisher) == "" {
		return out, fmt.Errorf("发布者不可为空")
	}
	return out, nil
}
func SigningBytes(p Payload) []byte {
	return append([]byte("AIDock artifact origin v1\n"), bytesOf(p)...)
}

func Verify(p pluginlock.Plugin, keys Keys, envelope *Envelope, now time.Time) Check {
	sub := For(p)
	out := Check{Name: p.Name, SubjectSHA256: sub.SHA256, Status: "UNKNOWN", Checks: []inventory.Finding{}}
	add := func(status, code, detail string) {
		out.Status = status
		out.Checks = append(out.Checks, inventory.Finding{Status: status, Code: code, Detail: detail})
	}
	if envelope == nil {
		add("UNKNOWN", "MCP_ORIGIN_EVIDENCE_MISSING", "缺少绑定当前产物的来源签名声明")
		return out
	}
	// Public callers can construct typed values without using the parsers.
	// Revalidate the entire contract before interpreting or verifying any claim.
	normalized, err := ParseKeys(JSON(keys))
	if err != nil {
		add("FAIL", "MCP_ORIGIN_KEYS_INVALID", err.Error())
		return out
	}
	keys = normalized
	e, err := ParseEnvelope(JSON(envelope))
	if err != nil {
		add("FAIL", "MCP_ORIGIN_EVIDENCE_INVALID", err.Error())
		return out
	}
	out.KeyID = e.KeyID
	out.Publisher = e.Payload.Publisher
	out.ExpiresAt = e.Payload.ExpiresAt
	var key *Key
	for i := range keys.Keys {
		if keys.Keys[i].ID == e.KeyID {
			key = &keys.Keys[i]
			break
		}
	}
	if key == nil {
		add("UNKNOWN", "MCP_ORIGIN_KEY_UNKNOWN", "签名密钥尚未在当前项目显式登记")
		return out
	}
	out.KeyLabel = key.Label
	if !key.Enabled {
		add("FAIL", "MCP_ORIGIN_KEY_DISABLED", "签名密钥已停用，旧声明不能继续作为有效来源证据")
		return out
	}
	expires, err := time.Parse(time.RFC3339, key.ExpiresAt)
	if err != nil || !now.Before(expires) {
		add("UNKNOWN", "MCP_ORIGIN_KEY_EXPIRED", "登记密钥已过期，需重新核对来源及有效期")
		return out
	}
	if key.SourceType != sub.SourceType || key.SourceID != sub.SourceID {
		add("FAIL", "MCP_ORIGIN_KEY_SCOPE_MISMATCH", "签名密钥不属于此产物来源范围")
		return out
	}
	if e.Payload.SubjectSHA256 != sub.SHA256 {
		add("FAIL", "MCP_ORIGIN_SUBJECT_MISMATCH", "签名声明与完整产物、版本、平台、依赖或运行时绑定不符")
		return out
	}
	pub, err := base64.StdEncoding.DecodeString(key.PublicKey)
	sig, sigErr := base64.StdEncoding.DecodeString(e.Signature)
	if err != nil || sigErr != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, SigningBytes(e.Payload), sig) {
		add("FAIL", "MCP_ORIGIN_SIGNATURE_INVALID", "来源声明的 Ed25519 签名无效")
		return out
	}
	issued, issuedErr := time.Parse(time.RFC3339, e.Payload.IssuedAt)
	expires, expiresErr := time.Parse(time.RFC3339, e.Payload.ExpiresAt)
	if issuedErr != nil || expiresErr != nil || now.Before(issued) || !now.Before(expires) {
		add("UNKNOWN", "MCP_ORIGIN_EVIDENCE_EXPIRED", "已验签声明尚未生效或已过期；缓存保留，不能继续复用有效结论")
		return out
	}
	out.RuntimeVersionVerified = e.Payload.RuntimeVersionVerified
	add("PASS", "MCP_ORIGIN_SIGNATURE_VERIFIED", "来源声明已由当前项目登记密钥验证；这证明登记密钥签署了此产物，发布者身份依赖登记时的独立核对")
	return out
}

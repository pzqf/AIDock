package pluginsecurity

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/pluginorigin"
)

// Assessment authorities are enrolled separately from artifact publishers.
// Enrollment records a user's independent identity check, not an automatic CA.
type Authority struct {
	pluginorigin.Key
	Provider        string `json:"provider"`
	VerificationURL string `json:"verification_url"`
}
type Authorities struct {
	Version     int         `json:"schema_version"`
	Authorities []Authority `json:"authorities"`
}
type Claim struct {
	Status      string `json:"status"`
	Method      string `json:"method"`
	Detail      string `json:"detail"`
	EvidenceURL string `json:"evidence_url"`
}
type AssessmentPayload struct {
	Version           int      `json:"schema_version"`
	Format            string   `json:"format"`
	Reference         Snapshot `json:"reference"`
	Coverage          string   `json:"coverage"`
	Malware           *Claim   `json:"malware"`
	StaticAnalysis    *Claim   `json:"static_analysis"`
	PublisherIdentity *Claim   `json:"publisher_identity"`
}
type Assessment struct {
	Version   int               `json:"schema_version"`
	KeyID     string            `json:"key_id"`
	Payload   AssessmentPayload `json:"payload"`
	Signature string            `json:"signature"`
}
type AssessmentResult struct {
	Status        string              `json:"status"`
	Authenticated bool                `json:"authenticated"`
	Current       bool                `json:"current"`
	KeyID         string              `json:"key_id,omitempty"`
	KeyLabel      string              `json:"key_label,omitempty"`
	TrustLevel    string              `json:"trust_level"`
	Report        *Assessment         `json:"report,omitempty"`
	Checks        []inventory.Finding `json:"checks"`
}

const AssessmentCoverage = "complete-artifact-runtime-and-resolved-dependencies-v1"

func ParseAuthorities(b []byte) (Authorities, error) {
	var out Authorities
	if len(b) > 1<<20 {
		return out, fmt.Errorf("评估者公钥清单超过 1 MB")
	}
	if err := decode(b, &out); err != nil {
		return out, err
	}
	if out.Version != 1 || out.Authorities == nil || len(out.Authorities) > 100 {
		return out, fmt.Errorf("评估者公钥清单格式无效")
	}
	keys := pluginorigin.Keys{Version: 1, Keys: []pluginorigin.Key{}}
	for _, k := range out.Authorities {
		if strings.TrimSpace(k.Provider) == "" || len(k.Provider) > 200 || !https(k.VerificationURL) {
			return out, fmt.Errorf("评估者名称或独立核对地址无效")
		}
		keys.Keys = append(keys.Keys, k.Key)
	}
	normalized, err := pluginorigin.ParseKeys(pluginorigin.JSON(keys))
	if err != nil {
		return out, err
	}
	for i := range out.Authorities {
		out.Authorities[i].Key = normalized.Keys[i]
	}
	return out, nil
}
func ParseAssessment(b []byte) (Assessment, error) {
	var out Assessment
	if len(b) > 2<<20 {
		return out, fmt.Errorf("外部评估超过 2 MB")
	}
	if err := decode(b, &out); err != nil {
		return out, err
	}
	if out.Version != 1 || !hash(out.KeyID) || out.Payload.Version != 1 || out.Payload.Format != "aidock-plugin-assessment-v1" || out.Payload.Coverage != AssessmentCoverage {
		return out, fmt.Errorf("外部评估格式、密钥或完整覆盖范围无效")
	}
	if _, err := Parse(JSON(out.Payload.Reference)); err != nil {
		return out, err
	}
	for _, c := range []*Claim{out.Payload.Malware, out.Payload.StaticAnalysis, out.Payload.PublisherIdentity} {
		if c == nil || c.Status != "PASS" && c.Status != "FAIL" && c.Status != "UNKNOWN" || strings.TrimSpace(c.Method) == "" || len(c.Method) > 500 || strings.TrimSpace(c.Detail) == "" || len(c.Detail) > 4000 || !https(c.EvidenceURL) {
			return out, fmt.Errorf("每项评估须明确 PASS/FAIL/UNKNOWN、方法版本、说明与证据地址")
		}
	}
	sig, err := base64.StdEncoding.DecodeString(out.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize || base64.StdEncoding.EncodeToString(sig) != out.Signature {
		return out, fmt.Errorf("评估签名须为规范 Base64 Ed25519 签名")
	}
	return out, nil
}
func AssessmentSigningBytes(p AssessmentPayload) []byte {
	return append([]byte("AIDock plugin assessment v1\n"), bytesOf(p)...)
}
func AssessmentTemplate(c Candidate, now time.Time) AssessmentPayload {
	unknown := func() *Claim { return &Claim{Status: "UNKNOWN"} }
	return AssessmentPayload{Version: 1, Format: "aidock-plugin-assessment-v1", Reference: Template(c, now), Coverage: AssessmentCoverage, Malware: unknown(), StaticAnalysis: unknown(), PublisherIdentity: unknown()}
}

// Signature verification authenticates a registered evaluator's claims; it does
// not execute scanners or prove that the evaluator's statements are true.
func VerifyAssessment(c Candidate, keys Authorities, report *Assessment, now time.Time) AssessmentResult {
	out := AssessmentResult{Status: "UNKNOWN", TrustLevel: "L0", Checks: []inventory.Finding{}}
	add := func(status, code, detail string) {
		out.Checks = append(out.Checks, inventory.Finding{Status: status, Code: code, Detail: detail})
		if status == "FAIL" || out.Status != "FAIL" && status == "UNKNOWN" {
			out.Status = status
		}
	}
	if report == nil {
		add("UNKNOWN", "MCP_SECURITY_ASSESSMENT_MISSING", "缺少当前产物的独立评估；本地漏洞参考不替代恶意代码、静态分析和身份核实")
		return out
	}
	out.Report = report
	parsed, err := ParseAssessment(JSON(report))
	if err != nil {
		add("FAIL", "MCP_ASSESSMENT_INVALID", err.Error())
		return out
	}
	out.KeyID = parsed.KeyID
	keys, err = ParseAuthorities(JSON(keys))
	if err != nil {
		add("FAIL", "MCP_ASSESSMENT_AUTHORITIES_INVALID", err.Error())
		return out
	}
	var key *Authority
	for i := range keys.Authorities {
		if keys.Authorities[i].ID == parsed.KeyID {
			key = &keys.Authorities[i]
			break
		}
	}
	if key == nil {
		add("UNKNOWN", "MCP_ASSESSMENT_AUTHORITY_UNKNOWN", "评估者密钥未在当前项目独立登记；来源签名公钥不会自动成为评估者")
		return out
	}
	out.KeyLabel = key.Label
	pub, _ := base64.StdEncoding.DecodeString(key.PublicKey)
	sig, _ := base64.StdEncoding.DecodeString(parsed.Signature)
	if !ed25519.Verify(pub, AssessmentSigningBytes(parsed.Payload), sig) {
		add("FAIL", "MCP_ASSESSMENT_SIGNATURE_INVALID", "外部评估 Ed25519 签名无效，不能解释其中的通过声明")
		return out
	}
	kind, id := pluginorigin.Source(c.Plugin)
	if key.SourceType != kind || key.SourceID != id || key.Provider != parsed.Payload.Reference.Provider {
		add("FAIL", "MCP_ASSESSMENT_AUTHORITY_SCOPE", "评估者不在此来源范围内，或报告署名与登记不符")
		return out
	}
	out.Authenticated = true
	if err = Bound(parsed.Payload.Reference, c); err != nil {
		add("UNKNOWN", "MCP_ASSESSMENT_STALE", err.Error())
		return out
	}
	out.Status = "PASS"
	ref := Verify(c, &parsed.Payload.Reference, now)
	for _, f := range ref.Checks {
		if f.Code != "MCP_SECURITY_ASSESSMENT_INCOMPLETE" {
			if f.Code == "MCP_SECURITY_REFERENCE_NO_MATCH" {
				f.Detail = "已登记评估者的签名参考在当前版本及直接声明依赖中未记录漏洞；这不是未来安全或无未知漏洞的保证"
			}
			add(f.Status, f.Code, f.Detail)
		}
	}
	for i, claim := range []*Claim{parsed.Payload.Malware, parsed.Payload.StaticAnalysis, parsed.Payload.PublisherIdentity} {
		label := []string{"恶意代码检查", "静态分析", "发布者身份核实"}[i]
		add(claim.Status, []string{"MCP_ASSESSMENT_MALWARE", "MCP_ASSESSMENT_STATIC", "MCP_ASSESSMENT_IDENTITY"}[i], label+"："+claim.Detail)
	}
	expires, _ := time.Parse(time.RFC3339, key.ExpiresAt)
	out.Current = key.Enabled && now.Before(expires) && ref.Current
	if !key.Enabled {
		add("FAIL", "MCP_ASSESSMENT_AUTHORITY_DISABLED", "评估者已停用；旧通过不能继续使用，已记录风险保留")
	} else if !now.Before(expires) {
		add("UNKNOWN", "MCP_ASSESSMENT_AUTHORITY_EXPIRED", "评估者公钥已过期；旧通过不可复用，已记录风险保留")
	}
	if !out.Current || out.Status == "FAIL" || ref.CVEStatus != "NO_KNOWN_MATCHES" || parsed.Payload.Malware.Status != "PASS" {
		return out
	}
	if !c.SourceVerified || !c.RuntimeVerified {
		add("UNKNOWN", "MCP_ASSESSMENT_ORIGIN_REQUIRED", "独立评估已核验；仍需有效来源签名和运行时版本声明，未验证来源保持 L0")
		return out
	}
	out.TrustLevel = "L1"
	if c.DependenciesVerified && parsed.Payload.StaticAnalysis.Status == "PASS" && parsed.Payload.PublisherIdentity.Status == "PASS" {
		out.TrustLevel = "L2"
	}
	add("PASS", "MCP_ASSESSMENT_TRUST", "根据当前项目登记的评估者、完整绑定及逐项证据计算为 "+out.TrustLevel+"；不授予项目运行权限或 OS 隔离能力，L3/L4 未验证")
	return out
}

func effective(c Candidate, local *Snapshot, keys Authorities, report *Assessment, now time.Time) Check {
	out := Verify(c, local, now)
	out.ReferenceSource = "local-reference"
	assessment := VerifyAssessment(c, keys, report, now)
	if assessment.Authenticated && report != nil && Bound(report.Payload.Reference, c) == nil {
		old := out
		out = Verify(c, &report.Payload.Reference, now)
		out.ReferenceSource = "signed-assessment"
		filtered := []inventory.Finding{}
		for _, f := range out.Checks {
			if f.Code != "MCP_SECURITY_ASSESSMENT_INCOMPLETE" {
				if f.Code == "MCP_SECURITY_REFERENCE_NO_MATCH" {
					f.Detail = "已登记评估者的签名参考在当前版本及直接声明依赖中未记录漏洞；这不是未来安全或无未知漏洞的保证"
				}
				filtered = append(filtered, f)
			}
		}
		out.Checks = filtered
		if old.Status == "FAIL" {
			out = old
			out.ReferenceSource = "local-reference"
		}
	}
	out.Assessment = assessment
	seen := map[inventory.Finding]bool{}
	for _, f := range out.Checks {
		seen[f] = true
	}
	for _, f := range out.Assessment.Checks {
		if !seen[f] {
			out.Checks = append(out.Checks, f)
			seen[f] = true
		}
		if f.Status == "FAIL" {
			out.Status = "FAIL"
		}
	}
	if out.Status != "FAIL" {
		out.TrustLevel = out.Assessment.TrustLevel
		if out.TrustLevel == "L2" && out.Assessment.Status == "PASS" {
			out.Status = "PASS"
		}
	}
	return out
}

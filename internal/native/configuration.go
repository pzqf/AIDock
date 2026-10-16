package native

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/nativeconfig"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type ConfigurationField struct {
	nativeconfig.Field
	Ownership  string `json:"ownership"`
	PrefixOnly bool   `json:"aidock_prefix"`
}

type ConfigurationFile struct {
	Tool           string                `json:"tool"`
	AdapterVersion string                `json:"adapter_version"`
	Contract       nativeconfig.Contract `json:"contract"`
	Present        bool                  `json:"present"`
	State          string                `json:"state"`
	SHA256         string                `json:"sha256,omitempty"`
	Bytes          int                   `json:"bytes"`
	Fields         []ConfigurationField  `json:"fields"`
	Checks         []inventory.Finding   `json:"checks"`
	WriteSupported bool                  `json:"write_supported"`
}

type ConfigurationReport struct {
	Root     string              `json:"root"`
	Project  string              `json:"project"`
	Scope    string              `json:"scope_id"`
	Copy     string              `json:"copy,omitempty"`
	Created  string              `json:"created"`
	Status   string              `json:"status"`
	Files    []ConfigurationFile `json:"files"`
	Boundary string              `json:"boundary"`
}

// Configuration reads only the selected adapters' fixed project files. It does
// not capture their potentially sensitive contents into the blob store, read
// user homes, expand variables, invoke tools, or save a diagnostic record.
func (m *Manager) Configuration(ctx context.Context, tools []string) (ConfigurationReport, error) {
	as, err := selectAdapters(tools)
	if err != nil {
		return ConfigurationReport{}, err
	}
	ledger, ledgerErr := m.connectionLedger()
	out := ConfigurationReport{Root: m.Root, Project: m.Source.Owner, Scope: m.Source.Scope, Copy: m.Source.Copy, Created: time.Now().UTC().Format(time.RFC3339Nano), Status: "OBSERVED", Files: []ConfigurationFile{}, Boundary: "仅检查当前绑定项目的原生 MCP 文件语法、服务器名称与字段归属；不读取用户级或企业级配置，不展开环境变量，不启动进程，不保存文件正文或凭据值。只有当前项目副本登记且字段摘要匹配的项才标记托管，名称前缀和注释不构成接管凭据。配置存在或语法可读均不证明工具加载、已授权、隔离或运行；连接安装、移除与回退需分别预览确认。"}
	for _, a := range as {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if a.MCPConfig == nil {
			return out, result.Fail("CONFIG_CONTRACT_UNSUPPORTED", "适配器没有登记项目 MCP 字段合同", 4)
		}
		file := ConfigurationFile{Tool: a.ID, AdapterVersion: a.Version, Contract: *a.MCPConfig, State: "ABSENT", Fields: []ConfigurationField{}, Checks: []inventory.Finding{}}
		if ledgerErr != nil {
			file.Checks = append(file.Checks, inventory.Finding{Status: "FAIL", Code: "MCP_CONNECTION_OWNERSHIP_INVALID", Detail: "当前项目副本字段归属记录无效；不接管或覆盖原生文件。"})
			out.Status = "FAIL"
		}
		data, present, err := readConfiguration(filepath.Join(m.Root, filepath.FromSlash(file.Contract.Path)))
		file.Present = present
		if err != nil {
			file.State, out.Status = "INVALID", "FAIL"
			code := nativeconfig.DescribeError(err)
			file.Checks = append(file.Checks, inventory.Finding{Status: "FAIL", Code: code, Detail: configurationIssue(code)})
		} else if !present {
			file.Checks = append(file.Checks, inventory.Finding{Status: "PASS", Code: "NATIVE_CONFIG_ABSENT", Detail: "该项目配置文件不存在；用户级、企业级和其它目录未检查，不据此认定没有 MCP。"})
			if len(ledger.Tools[a.ID].Fields) > 0 && ledgerErr == nil {
				file.State, out.Status = "DRIFT", "FAIL"
				file.Checks = append(file.Checks, inventory.Finding{Status: "FAIL", Code: "CONFIG_MANAGED_DRIFT", Detail: "已登记连接的原生文件不存在；保留归属记录，重新协调后再安装。"})
			}
		} else {
			file.SHA256, file.Bytes = workstore.Hash(data), len(data)
			observation, parseErr := nativeconfig.Inspect(file.Contract, data)
			if parseErr != nil {
				file.State, out.Status = "INVALID", "FAIL"
				code := nativeconfig.DescribeError(parseErr)
				file.Checks = append(file.Checks, inventory.Finding{Status: "FAIL", Code: code, Detail: configurationIssue(code)})
			} else {
				file.State = "READABLE"
				file.Checks = append(file.Checks, inventory.Finding{Status: "PASS", Code: "NATIVE_CONFIG_READABLE", Detail: "项目配置语法与服务器对象结构可读取；未连接服务器或检查工具实际加载。"})
				for _, field := range observation.Fields {
					prefix := strings.HasPrefix(strings.ToLower(field.Name), "aidock-")
					ownership := "UNMANAGED"
					if owned, ok := ledger.Tools[a.ID].Fields[field.Name]; ok && ledgerErr == nil {
						ownership = "MANAGED"
						file.WriteSupported = true
						if field.SHA256 != owned.SHA256 {
							ownership = "DRIFT"
							file.State, out.Status = "DRIFT", "FAIL"
							file.Checks = append(file.Checks, inventory.Finding{Status: "FAIL", Code: "CONFIG_MANAGED_DRIFT", Detail: "托管字段已被外部修改；保留手改，重新协调后再安装。"})
						}
					}
					file.Fields = append(file.Fields, ConfigurationField{Field: field, Ownership: ownership, PrefixOnly: prefix})
					if prefix && ownership == "UNMANAGED" {
						file.Checks = append(file.Checks, inventory.Finding{Status: "WARN", Code: "NATIVE_CONFIG_UNOWNED_NAMESPACE", Detail: "发现 aidock- 前缀的未登记项；前缀不能证明由本项目生成，同名受管写入必须先显式协调。"})
					}
				}
				for name := range ledger.Tools[a.ID].Fields {
					found := false
					for _, field := range observation.Fields {
						found = found || field.Name == name
					}
					if !found && ledgerErr == nil {
						file.State, out.Status = "DRIFT", "FAIL"
						file.Checks = append(file.Checks, inventory.Finding{Status: "FAIL", Code: "CONFIG_MANAGED_DRIFT", Detail: "已登记的托管字段已被移除；保留手改与归属记录，重新协调后再安装。"})
					}
				}
			}
		}
		out.Files = append(out.Files, file)
	}
	return out, nil
}

func configurationIssue(code string) string {
	switch code {
	case "CONFIG_DUPLICATE_KEY":
		return "JSON 中存在重复字段，工具可能采用不同的值。请先删除重复定义，再重新检查；原文件保留。"
	case "CONFIG_JSON_INVALID":
		return "JSON 语法不完整或无效。请检查逗号、引号和括号，再重新检查；原文件保留。"
	case "CONFIG_TOML_INVALID":
		return "TOML 语法无效或同一字段、表被重复定义。请修复后重新检查；原文件保留。"
	case "CONFIG_NAMESPACE_INVALID", "CONFIG_SERVER_INVALID", "CONFIG_OBJECT_REQUIRED":
		return "MCP 配置需要以独立服务器对象组织，当前字段结构不符合适配器合同；原文件保留。"
	case "CONFIG_MARKER_INVALID":
		return "托管标记缺失配对、包含其它字段或与服务器边界不一致，无法确定归属；原文件保留。"
	case "CONFIG_PATH_UNSAFE":
		return "配置路径包含链接、重解析点或目录，无法按普通项目文件安全读取；原路径保留。"
	case "CONFIG_TOO_LARGE":
		return "项目配置超过 1 MiB，超出本次字段检查范围；原文件保留。"
	case "CONFIG_ENCODING_INVALID":
		return "配置包含无效的 UTF-8 文本或 Unicode 转义。请核对文件编码后重新检查；原文件保留。"
	default:
		return "项目配置无法读取或超出当前解析范围。请核对文件权限与内容后重新检查；原文件保留。"
	}
}

func readConfiguration(path string) ([]byte, bool, error) {
	if err := workstore.CheckLinks(path); err != nil {
		return nil, false, &nativeconfig.Error{Code: "CONFIG_PATH_UNSAFE"}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, true, &nativeconfig.Error{Code: "CONFIG_PATH_UNSAFE"}
	}
	if info.Size() > nativeconfig.MaxBytes {
		return nil, true, &nativeconfig.Error{Code: "CONFIG_TOO_LARGE"}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, true, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, nativeconfig.MaxBytes+1))
	if err != nil {
		return nil, true, err
	}
	if len(data) > nativeconfig.MaxBytes {
		return nil, true, &nativeconfig.Error{Code: "CONFIG_TOO_LARGE"}
	}
	return data, true, nil
}

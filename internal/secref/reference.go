// Package secref validates credential references without resolving or exposing values.
package secref

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var name = regexp.MustCompile("^[a-z][a-z0-9_]{0,63}$")
var variable = regexp.MustCompile("^[A-Z][A-Z0-9_]{0,63}$")
var placeholder = regexp.MustCompile(`^\$\{SECRET:([a-z][a-z0-9_]{0,63})\}$`)

type Reference struct {
	Variable string `json:"variable"`
	Name     string `json:"name"`
}

func ValidName(s string) bool { return name.MatchString(s) }
func ValidVariable(s string) bool {
	if !variable.MatchString(s) {
		return false
	}
	for _, prefix := range []string{"AIDOCK_", "LD_", "DYLD_", "NODE_", "PYTHON", "RUBY", "PERL", "BASH_", "GIT_", "NPM_", "YARN_", "DOTNET_", "COMPLUS_", "JAVA_", "JDK_", "_JAVA_", "GOTOOL", "SSL_", "OPENSSL_"} {
		if strings.HasPrefix(s, prefix) {
			return false
		}
	}
	switch s {
	case "PATH", "PATHEXT", "SYSTEMROOT", "WINDIR", "COMSPEC", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP", "ENV", "SHELLOPTS", "CDPATH", "IFS", "CLASSPATH", "PSMODULEPATH", "ELECTRON_RUN_AS_NODE", "GOFLAGS", "GOROOT", "GOPATH", "GOENV", "GOWORK", "GOMOD", "GOCACHE":
		return false
	}
	return true
}
func invalid() error {
	return fmt.Errorf("MCP 凭据配置无效：secrets 只接受唯一名称；env 只接受大写变量名与完整的 SECRET 占位符，引用须已声明；禁止系统和运行时控制变量")
}

// Definition accepts an untrusted parsed MCP server. Errors never echo input.
func Definition(s map[string]any) ([]Reference, error) {
	names := map[string]bool{}
	if raw, ok := s["secrets"]; ok {
		a, ok := raw.([]any)
		if !ok || len(a) > 64 {
			return nil, invalid()
		}
		for _, raw := range a {
			n, ok := raw.(string)
			if !ok || !ValidName(n) || names[n] {
				return nil, invalid()
			}
			names[n] = true
		}
	}
	out := []Reference{}
	if raw, ok := s["env"]; ok {
		env, ok := raw.(map[string]any)
		if !ok || len(env) > 64 {
			return nil, invalid()
		}
		for key, raw := range env {
			value, ok := raw.(string)
			if !ok || !ValidVariable(key) {
				return nil, invalid()
			}
			match := placeholder.FindStringSubmatch(value)
			if len(match) != 2 || !names[match[1]] {
				return nil, invalid()
			}
			out = append(out, Reference{key, match[1]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Variable < out[j].Variable })
	return out, nil
}
func Names(refs []Reference) []string {
	set := map[string]bool{}
	for _, r := range refs {
		set[r.Name] = true
	}
	out := []string{}
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

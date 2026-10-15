package secref

import (
	"strings"
	"testing"
)

func TestDefinitionExactDeclaredReferences(t *testing.T) {
	s := map[string]any{"secrets": []any{"alpha", "unused"}, "env": map[string]any{"SECOND_TOKEN": "${SECRET:alpha}", "API_KEY": "${SECRET:alpha}"}}
	refs, err := Definition(s)
	if err != nil || len(refs) != 2 || refs[0].Variable != "API_KEY" || len(Names(refs)) != 1 {
		t.Fatal(refs, err)
	}
	if !ValidVariable("GOOGLE_API_KEY") {
		t.Fatal("ordinary service key blocked")
	}
}
func TestDefinitionRejectsUnsafeMappingsWithoutEcho(t *testing.T) {
	for _, raw := range []any{nil, []any{"private-sentinel"}, map[string]any{"API_KEY": "private-sentinel"}, map[string]any{"API_KEY": "prefix${SECRET:key}"}, map[string]any{"API_KEY": "${SECRET:missing}"}, map[string]any{"PATH": "${SECRET:key}"}, map[string]any{"NODE_OPTIONS": "${SECRET:key}"}, map[string]any{"private-sentinel": "${SECRET:key}"}, map[string]any{"API_KEY": nil}} {
		_, err := Definition(map[string]any{"secrets": []any{"key"}, "env": raw})
		if err == nil || strings.Contains(err.Error(), "private-sentinel") {
			t.Fatal("unsafe mapping accepted or echoed", err)
		}
	}
	for _, n := range []string{"SystemRoot", "COMSPEC", "PYTHONPATH", "LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "AIDOCK_HOME", "BASH_ENV", "GIT_CONFIG", "RUBYOPT", "PERL5OPT", "DOTNET_STARTUP_HOOKS", "JAVA_TOOL_OPTIONS", "GOFLAGS", "SSL_CERT_FILE"} {
		if ValidVariable(n) {
			t.Fatal("runtime control accepted", n)
		}
	}
}

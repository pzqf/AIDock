package source

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginconfig"
	"github.com/pzqf/AIDock/internal/pluginmanifest"
	"github.com/pzqf/AIDock/internal/profile"
)

func configuredSessionRequest(t *testing.T, m *Manager, name string) MCPSessionRequest {
	t.Helper()
	ctx := context.Background()
	t.Setenv(pluginconfig.Variable, "PARENT_CONFIG_MUST_NOT_BE_DELIVERED")
	grant := grantPermission(name)
	grant.ConfirmUnisolated = true
	grant.ConfirmTools = true
	permissionApply(t, m, permissionPlan(t, m, grant))
	p, err := m.MCPPermission(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.CheckMCPRuntime(ctx, MCPRuntimeRequest{Name: name, Probe: true, ExpectedSHA256: p.SHA256, ExpectedReviewSHA256: p.ReviewSHA256})
	if err != nil || r.Code != "RUNTIME_VERSION_MATCH" {
		t.Fatal(r, err)
	}
	return MCPSessionRequest{Name: name, Probe: true, ExpectedSHA256: p.SHA256, ExpectedReviewSHA256: p.ReviewSHA256, ExpectedRuntimeAttempt: r.Attempt}
}

func TestProfilePluginConfigurationDeliveredOnlyToExactAuthorizedSession(t *testing.T) {
	m, req := sessionFixtureWithManifest(t, func(body string) string {
		return `const configured=JSON.parse(process.env.AIDOCK_PLUGIN_CONFIG||'null');if(!configured||configured.message!=='PRIVATE_CONFIG_DELIVERY')process.exit(95);` + body
	}, func(manifest map[string]any) {
		manifest["config_delivery"] = pluginconfig.Delivery
		manifest["config_schema"] = map[string]any{"message": map[string]any{"type": "string", "default": "PRIVATE_CONFIG_DELIVERY"}}
	})
	ctx := context.Background()
	before := capture(t, m)
	if e, err := m.CheckMCPSession(ctx, req); err != nil || e.Code != "MCP_SESSION_MATCH" || e.Input.ConfigurationSHA256 == "" {
		t.Fatal(e, err)
	}
	unchangedManager(t, m, before)
	p, err := m.Profile()
	if err != nil || len(p.MCP) != 1 || p.MCP[0].Configuration == nil || p.MCP[0].Configuration.Status != "PASS" || !p.MCP[0].Configuration.DeliveryObserved {
		t.Fatal(p.MCP, err)
	}
	// A mapped plugin selector controls the same registered MCP. The effective
	// config is saved independently; it cannot inherit the prior grant.
	p.Profile["plugins"] = map[string]any{"enable": []any{req.Name}}
	p.Profile["config_overrides"] = map[string]any{req.Name: map[string]any{"message": "NEW_PRIVATE_CONFIGURATION"}}
	plan, err := m.ProfilePlan(ctx, ProfileEdit{ExpectedSHA256: p.SHA256, Profile: p.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(ctx, plan.Plan.ID, plan.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err = m.CheckMCPSession(ctx, req); err == nil {
		t.Fatal("configuration change reused old grant")
	}
	p, err = m.Profile()
	if err != nil || !p.MCP[0].EffectiveEnabled || p.MCP[0].Configuration == nil || p.MCP[0].Configuration.SHA256 == "" || p.MCP[0].Configuration.DeliveryObserved {
		t.Fatal(p.MCP, err)
	}
	newReq := configuredSessionRequest(t, m, req.Name)
	e, err := m.CheckMCPSession(ctx, newReq)
	if err != nil || e.Code == "MCP_SESSION_MATCH" {
		t.Fatal("fixture must reject the newly delivered value, rather than observe a retained old environment", e, err)
	}
	sessions, err := m.MCPSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(sessions)
	if strings.Contains(string(encoded), "PRIVATE_CONFIG_DELIVERY") || strings.Contains(string(encoded), "NEW_PRIVATE_CONFIGURATION") {
		t.Fatal("configuration value entered audit")
	}
	// Restoring the literal value is a fresh explicit grant, never old authority.
	p, err = m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	p.Profile["config_overrides"] = map[string]any{req.Name: map[string]any{"message": "PRIVATE_CONFIG_DELIVERY"}}
	plan, err = m.ProfilePlan(ctx, ProfileEdit{ExpectedSHA256: p.SHA256, Profile: p.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(ctx, plan.Plan.ID, plan.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	restored := configuredSessionRequest(t, m, req.Name)
	if e, err = m.CheckMCPSession(ctx, restored); err != nil || e.Code != "MCP_SESSION_MATCH" {
		t.Fatal(e, err)
	}
}

func TestProfilePluginAliasAndConfigurationReferencesPreventDefinitionRemoval(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "test"})
	saveMCPDefinition(t, m, false)
	ctx := context.Background()
	p, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	p.Profile["plugins"] = map[string]any{"disable": []any{"project-memory"}}
	plan, err := m.ProfilePlan(ctx, ProfileEdit{ExpectedSHA256: p.SHA256, Profile: p.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(ctx, plan.Plan.ID, plan.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	s, err := m.MCP()
	if err != nil {
		t.Fatal(err)
	}
	s.Config["servers"] = []any{}
	_, err = m.MCPPlan(ctx, MCPEdit{ExpectedSHA256: s.SHA256, Config: s.Config})
	code(t, err, "MCP_PROFILE_REFERENCE_EXISTS")
}

func TestPluginConfigurationExplicitRemovalDoesNotRestoreSchemaDefault(t *testing.T) {
	m := manager(t)
	manifest := &pluginmanifest.Manifest{ConfigDelivery: pluginconfig.Delivery, ConfigSchema: map[string]any{"message": map[string]any{"type": "string", "default": "default must remain absent"}}}
	// These are compilation inputs, not proof of installation or execution.
	lock := MCPLockState{Manifests: []pluginmanifest.Report{{Name: "service", ArtifactStatus: "PASS", ManifestStatus: "PASS", Manifest: manifest}}}
	conf := map[string]any{"servers": []any{map[string]any{"name": "service"}}}
	for _, path := range []string{"/service/message", "/service"} {
		input := profile.Input{Project: map[string]any{"schema_version": 2, "name": "shared", "config_overrides": map[string]any{"service": map[string]any{"message": "shared"}}}, Local: map[string]any{"schema_version": 2, "name": "local", "config_remove": []any{path}}}
		report, err := profile.Resolve(input)
		if err != nil {
			t.Fatal(err)
		}
		compiled, checks := m.compilePluginConfigurations(conf, lock, &report)
		if len(checks) != 1 || checks[0].Status != "PASS" || string(compiled["service"].Data) != "{}" || len(compiled["service"].Omitted) != 1 {
			t.Fatal(compiled, checks)
		}
		input.Session = map[string]any{"schema_version": 2, "name": "session", "config_overrides": map[string]any{"service": map[string]any{"message": "reinstated"}}}
		report, err = profile.Resolve(input)
		if err != nil {
			t.Fatal(err)
		}
		compiled, checks = m.compilePluginConfigurations(conf, lock, &report)
		if len(checks) != 1 || string(compiled["service"].Data) != `{"message":"reinstated"}` || len(compiled["service"].Omitted) != 0 {
			t.Fatal(compiled, checks)
		}
	}
}

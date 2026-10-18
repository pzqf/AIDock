package source

import (
	"context"
	"github.com/pzqf/AIDock/internal/pluginartifact"
	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/workstore"
)

func (m *Manager) CandidateArtifacts(ctx context.Context, c ConfigurationCandidate) (MCPArtifactState, error) {
	if err := m.validateConfigurationCandidate(c); err != nil {
		return MCPArtifactState{}, err
	}
	return m.ReplayArtifacts(ctx, c.draft)
}

func (m *Manager) PlanCandidateArtifact(ctx context.Context, c ConfigurationCandidate, req pluginartifact.Edit) (pluginartifact.Preview, error) {
	if err := m.validateConfigurationCandidate(c); err != nil {
		return pluginartifact.Preview{}, err
	}
	return m.PlanReplayArtifact(ctx, c.draft, req)
}

func (m *Manager) ApplyCandidateArtifact(ctx context.Context, c ConfigurationCandidate, name, id, digest string) (workstore.Journal, error) {
	if err := m.validateConfigurationCandidate(c); err != nil {
		return workstore.Journal{}, err
	}
	return m.ApplyReplayArtifact(ctx, c.draft, name, id, digest)
}

func (m *Manager) CandidateInstallations(ctx context.Context, c ConfigurationCandidate) (plugininstall.State, error) {
	if err := m.validateConfigurationCandidate(c); err != nil {
		return plugininstall.State{}, err
	}
	return m.ReplayInstallations(ctx, c.draft)
}

func (m *Manager) PlanCandidateInstallation(ctx context.Context, c ConfigurationCandidate, req plugininstall.Edit) (plugininstall.Preview, error) {
	if err := m.validateConfigurationCandidate(c); err != nil {
		return plugininstall.Preview{}, err
	}
	return m.PlanReplayInstallation(ctx, c.draft, req)
}

func (m *Manager) ApplyCandidateInstallation(ctx context.Context, c ConfigurationCandidate, name, id, digest string) (workstore.Journal, error) {
	if err := m.validateConfigurationCandidate(c); err != nil {
		return workstore.Journal{}, err
	}
	return m.ApplyReplayInstallation(ctx, c.draft, name, id, digest)
}

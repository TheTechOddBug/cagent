package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSharedCommandsAndSkills_Resolve(t *testing.T) {
	t.Parallel()

	cfg, err := Load(t.Context(), NewFileSource("testdata/shared_commands_skills.yaml"))
	require.NoError(t, err)

	dev, ok := cfg.Agents.Lookup("dev")
	require.True(t, ok)

	// Commands from the "ci" and "docs" groups are merged in alongside the
	// agent's own inline commands.
	assert.Equal(t, "Run linter", dev.Commands["lint"].Instruction)
	assert.Equal(t, "Run the tests", dev.Commands["test"].Instruction)
	assert.Equal(t, "Build the documentation", dev.Commands["build-docs"].Instruction)

	// The agent's inline "deploy" wins over the group's "deploy".
	assert.Equal(t, "Custom deploy override", dev.Commands["deploy"].Instruction)

	// Skills: the "base" group ("local" source + "git-skill" name filter) is
	// merged with the agent's own "docker-skill" include filter.
	assert.Equal(t, []string{"local"}, dev.Skills.Sources)
	assert.ElementsMatch(t, []string{"docker-skill", "git-skill"}, dev.Skills.Include)

	// The same groups are reusable across agents.
	ops, ok := cfg.Agents.Lookup("ops")
	require.True(t, ok)
	assert.Equal(t, "Deploy the app", ops.Commands["deploy"].Instruction)
	assert.Equal(t, "Run the tests", ops.Commands["test"].Instruction)
	assert.Equal(t, []string{"local"}, ops.Skills.Sources)
	assert.Equal(t, []string{"git-skill"}, ops.Skills.Include)
}

func TestSharedCommands_MissingGroup(t *testing.T) {
	t.Parallel()

	_, err := Load(t.Context(), NewFileSource("testdata/shared_commands_missing.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-existent command group 'nonexistent'")
}

func TestSharedSkills_MissingGroup(t *testing.T) {
	t.Parallel()

	_, err := Load(t.Context(), NewFileSource("testdata/shared_skills_missing.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-existent skill group 'nonexistent'")
}

func TestSharedSkills_InvalidGroupValidated(t *testing.T) {
	t.Parallel()

	// Top-level skill groups are validated even when no agent references them.
	_, err := Load(t.Context(), NewFileSource("testdata/shared_skills_invalid_group.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "skill group 'bad' inline skill 'broken' is missing a description")
}

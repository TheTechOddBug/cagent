package config

import (
	"fmt"

	"github.com/docker/docker-agent/pkg/config/latest"
	"github.com/docker/docker-agent/pkg/config/types"
)

// resolveCommandDefinitions merges reusable command groups referenced by agents
// (via use_commands) from the top-level commands section into each agent's
// Commands map. Commands defined inline on the agent take precedence over a
// group's command of the same name, mirroring the override semantics used for
// MCP definitions (see applyMCPDefaults).
func resolveCommandDefinitions(cfg *latest.Config) error {
	for i := range cfg.Agents {
		agent := &cfg.Agents[i]
		for _, ref := range agent.UseCommands {
			group, ok := cfg.Commands[ref]
			if !ok {
				return fmt.Errorf("agent '%s' references non-existent command group '%s'", agent.Name, ref)
			}
			if agent.Commands == nil {
				agent.Commands = make(types.Commands, len(group))
			}
			for name, cmd := range group {
				if _, exists := agent.Commands[name]; !exists {
					agent.Commands[name] = cmd
				}
			}
		}
	}
	return nil
}

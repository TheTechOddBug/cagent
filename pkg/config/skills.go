package config

import (
	"fmt"
	"slices"

	"github.com/docker/docker-agent/pkg/config/latest"
)

// resolveSkillDefinitions merges reusable skill groups referenced by agents
// (via use_skills) from the top-level skills section into each agent's
// SkillsConfig. Every top-level group is validated up front (parity with
// resolveMCPDefinitions), then each referenced group is merged into the agent.
func resolveSkillDefinitions(cfg *latest.Config) error {
	for name := range cfg.Skills {
		group := cfg.Skills[name]
		if err := validateSkills(fmt.Sprintf("skill group '%s'", name), &group); err != nil {
			return err
		}
	}

	for i := range cfg.Agents {
		agent := &cfg.Agents[i]
		for _, ref := range agent.UseSkills {
			group, ok := cfg.Skills[ref]
			if !ok {
				return fmt.Errorf("agent '%s' references non-existent skill group '%s'", agent.Name, ref)
			}
			mergeSkills(&agent.Skills, group)
		}
	}
	return nil
}

// mergeSkills folds src into dst: Sources and Include are unioned while
// preserving order and dropping duplicates; inline skills are appended unless
// dst already defines one with the same name (the agent's inline skill wins).
func mergeSkills(dst *latest.SkillsConfig, src latest.SkillsConfig) {
	for _, s := range src.Sources {
		if !slices.Contains(dst.Sources, s) {
			dst.Sources = append(dst.Sources, s)
		}
	}
	for _, inc := range src.Include {
		if !slices.Contains(dst.Include, inc) {
			dst.Include = append(dst.Include, inc)
		}
	}
	seen := make(map[string]bool, len(dst.Inline))
	for _, in := range dst.Inline {
		seen[in.Name] = true
	}
	for _, in := range src.Inline {
		if !seen[in.Name] {
			dst.Inline = append(dst.Inline, in)
			seen[in.Name] = true
		}
	}
}

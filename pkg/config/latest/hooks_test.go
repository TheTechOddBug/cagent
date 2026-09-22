package latest

import (
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHooksValidateContracts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, config, want string }{
		{"matcher", `pre_tool_use: [{matcher: "[", hooks: [{type: command, command: true}]}]`, "invalid matcher"},
		{"policy", `turn_start: [{type: command, command: true, on_error: blok}]`, "on_error must"},
		{"negative timeout", `turn_start: [{type: command, command: true, timeout: -1}]`, "timeout must"},
		{"unsupported block", `stop: [{type: command, command: true, on_error: block}]`, "not supported"},
		{"invalid lane", `post_tool_use: [{preempt_yolo: false, hooks: [{type: command, command: true}]}]`, "only valid on pre_tool_use"},
		{"strict output", `before_llm_call: [{type: command, command: true, strict_output: true, on_error: block}]`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var cfg HooksConfig
			require.NoError(t, yaml.Unmarshal([]byte(tc.config), &cfg))
			err := cfg.Validate()
			if tc.want != "" {
				require.ErrorContains(t, err, tc.want)
			} else {
				require.NoError(t, err)
				assert.True(t, cfg.BeforeLLMCall[0].StrictOutput)
			}
		})
	}
}

func TestModelGuardConfiguration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		event, schema string
		valid         bool
	}{
		{"prompt_file_guard", "guard_decision", true},
		{"prompt_file_guard", "", false},
		{"prompt_file_guard", "pre_tool_use_decision", false},
		{"skill_content_guard", "guard_decision", true},
		{"skill_content_guard", "", false},
		{"skill_content_guard", "pre_tool_use_decision", false},
		{"post_tool_use", "guard_decision", true},
		{"post_tool_use", "pre_tool_use_decision", false},
		{"turn_start", "guard_decision", false},
		{"turn_start", "", true},
	} {
		t.Run(tc.event+"/"+tc.schema, func(t *testing.T) {
			t.Parallel()
			var cfg HooksConfig
			require.NoError(t, yaml.Unmarshal([]byte(tc.event+`: [{type: model, model: test/model, prompt: check, system_prompt: policy, schema: "`+tc.schema+`"}]`), &cfg))
			// Tool-matched events use matcher entries rather than a flat list.
			if tc.event == "post_tool_use" {
				cfg.PostToolUse = HookMatcherConfigs{{Hooks: HookDefinitions{{Type: "model", Model: "test/model", Prompt: "check", Schema: tc.schema}}}}
			}
			err := cfg.Validate()
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

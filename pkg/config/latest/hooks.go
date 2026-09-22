package latest

import (
	"fmt"
	"iter"
	"reflect"
	"strings"

	"github.com/docker/docker-agent/pkg/hooks/events"
)

// Events iterates over populated hook events using their persisted names.
func (h *HooksConfig) Events() iter.Seq2[string, HookMatcherConfigs] {
	return func(yield func(string, HookMatcherConfigs) bool) {
		if h == nil {
			return
		}
		v := reflect.ValueOf(h).Elem()
		for i := range v.NumField() {
			if v.Field(i).Len() == 0 {
				continue
			}
			name, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ",")
			var matchers HookMatcherConfigs
			switch hooks := v.Field(i).Interface().(type) {
			case HookDefinitions:
				matchers = HookMatcherConfigs{{Hooks: hooks}}
			case HookMatcherConfigs:
				matchers = hooks
			}
			if !yield(name, matchers) {
				return
			}
		}
	}
}

// IsEmpty reports whether no hook events are configured.
func (h *HooksConfig) IsEmpty() bool {
	for range h.Events() {
		return false
	}
	return true
}

// Validate checks hook definitions and event-specific options.
func (h *HooksConfig) Validate() error {
	for event, matchers := range h.Events() {
		contract, ok := events.Lookup(event)
		if !ok {
			return fmt.Errorf("hooks.%s: unknown event", event)
		}
		if !contract.CanBlock {
			for _, matcher := range matchers {
				for _, hook := range matcher.Hooks {
					if hook.OnError == "block" {
						return fmt.Errorf("hooks.%s: on_error block is not supported by this event", event)
					}
				}
			}
		}
		for _, matcher := range matchers {
			for _, hook := range matcher.Hooks {
				if hook.Type != "model" {
					continue
				}
				if hook.Schema == "guard_decision" && !contract.CanBlock {
					return fmt.Errorf("hooks.%s: guard_decision requires a blocking event", event)
				}
				if hook.Schema == "pre_tool_use_decision" && !contract.Permission() {
					return fmt.Errorf("hooks.%s: pre_tool_use_decision requires an approval event", event)
				}
				if (event == "skill_content_guard" || event == "prompt_file_guard") && hook.Schema != "guard_decision" {
					return fmt.Errorf("hooks.%s: model hooks require schema guard_decision", event)
				}
			}
		}
		for i, matcher := range matchers {
			for _, hook := range matcher.Hooks {
				if hook.Type == "evaluator" && event != "tool_guard" {
					return fmt.Errorf("hooks.%s: evaluator hooks are only supported on tool_guard", event)
				}
			}
			if contract.ToolMatched {
				if err := matcher.validate(event, i); err != nil {
					return err
				}
			} else {
				for j, hook := range matcher.Hooks {
					if err := hook.validate(event, j); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

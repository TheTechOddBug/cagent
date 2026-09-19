package hooks

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/docker/docker-agent/pkg/config/latest"
)

// ShapeGuardDecision is an explicit allow/deny verdict for blocking events.
const ShapeGuardDecision = "guard_decision"

var _ = registerGuardDecision()

func registerGuardDecision() bool {
	if err := errors.Join(
		RegisterResponseShape(ShapeGuardDecision, guardDecisionShape),
		RegisterResponseSchema(ShapeGuardDecision, &latest.StructuredOutput{
			Name:        ShapeGuardDecision,
			Description: "Allow or deny the operation",
			Strict:      true,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"decision": map[string]any{"type": "string", "enum": []string{"allow", "deny"}},
					"reason":   map[string]any{"type": "string"},
				},
				"required":             []string{"decision", "reason"},
				"additionalProperties": false,
			},
		}),
	); err != nil {
		panic(err)
	}
	return true
}

func guardDecisionShape(raw string, in *Input) (*Output, error) {
	if in == nil || !EventContract(in.HookEventName).CanBlock {
		return nil, errors.New("guard_decision requires a blocking event")
	}
	var verdict struct {
		Decision string  `json:"decision"`
		Reason   *string `json:"reason"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&verdict); err != nil {
		return nil, errors.New("invalid guard verdict")
	}
	if decoder.Decode(new(any)) != io.EOF || verdict.Reason == nil {
		return nil, errors.New("invalid guard verdict")
	}
	switch verdict.Decision {
	case "allow":
		return &Output{Continue: new(true)}, nil
	case "deny":
		return &Output{Decision: DecisionBlockValue, Reason: *verdict.Reason}, nil
	default:
		return nil, errors.New("guard verdict must be allow or deny")
	}
}

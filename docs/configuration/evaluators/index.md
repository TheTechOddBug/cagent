---
title: "Evaluators"
description: "Reusable provider-backed assessments, separate from chat models and decision policies."
keywords: docker agent, evaluators, typesafe, jev, classification, tool guards
weight: 65
canonical: https://docs.docker.com/ai/docker-agent/configuration/evaluators/
---

Evaluators assess input against fixed criteria and return typed results. They do
not generate chat messages or execute tools. Consumers decide what an assessment
means: an evaluator reports a probability; a tool guard decides whether to ask
for approval.

The first provider is [TypeSafe's Jev](https://docs.typesafe.ai/). Named evaluators
are shared across agents in a loaded team. Imported agents keep their source
configuration’s evaluator bindings, even when the parent uses the same names. Configurations using this feature
require version `16` or an omitted version (latest).

## Define an evaluator

```yaml
evaluators:
  credential_exposure:
    provider: typesafe
    model: jev-latest
    type: boolean
    instructions: Does the operation in tool_input disclose credentials outside a trusted boundary?
    timeout: 3s
```

| Field | Meaning |
| --- | --- |
| `provider` | Required backend type (`typesafe`) or named entry in `providers`. |
| `model` | Required provider model ID, such as `jev-latest`. Pin a versioned ID for reproducible evaluations. |
| `type` | Required: `boolean`, `choice`, or `score`. |
| `instructions` | Required assessment question or rubric instructions. |
| `choices` | For `choice`: map of 2–255 outcome keys to descriptions. |
| `levels` | For `score`: 2–10 descriptions ordered from lowest to highest. |
| `base_url` | Optional API base URL. TypeSafe defaults to `https://api.typesafe.ai`; `/v1/systemone` is appended. |
| `token_key` | Environment variable containing the API key; defaults to `TYPESAFE_API_KEY`. |
| `timeout` | Request timeout as a duration such as `3s`; defaults to `10s`. |

Connection defaults can be shared through a named provider:

```yaml
providers:
  assessments:
    provider: typesafe
    token_key: CORPORATE_TYPESAFE_KEY

evaluators:
  complexity:
    provider: assessments
    model: jev-latest
    type: choice
    instructions: Classify the reasoning needed to answer this request.
    choices:
      simple: Routine lookup, extraction, or localized editing.
      complex: Multi-step reasoning, architectural analysis, or difficult debugging.
      unknown: Not enough context to assess.
```

Evaluator-level `base_url` and `token_key` override provider defaults. Chat-only
provider settings do not apply; `api_type` and `auth` are rejected for evaluator
providers. Credentials come from the normal environment provider, including
configured secret sources. The models gateway does not supply evaluator credentials.

In HCL, use `evaluator "name" { ... }` for a top-level named evaluator.

## Result types

- **Boolean:** `probability` is the probability the statement is true. Mapped to
  TypeSafe's `noul` primitive; no confidence value is invented.
- **Choice:** `choice` identifies a highest-probability outcome, and
  `probabilities` contains the distribution across configured choices.
- **Score:** `score` is the expected zero-based level index. For three levels its
  range is 0–2, not 0–1. Probability keys are `"0"`, `"1"`, and `"2"`.

Results also carry the returned model ID, token usage, and optional provider
confidence. Confidence is distinct from outcome probability, and neither should
be assumed comparable across providers. Missing or invalid required answer fields
are errors, not zero-valued assessments.

The Go API is `evaluator.Evaluator.Evaluate(ctx, state)`. State can be a string,
JSON object, or array. Loaded teams expose named clients through `Team.Evaluator`.
The initial implementation sends one question per evaluation and does not retry
failed requests automatically. Evaluator usage is returned by the Go API but is
not yet included in session token/cost totals or run budget accounting.

## Tool guards

The first built-in consumer is a `type: evaluator` hook on `tool_guard`.
Boolean and choice evaluators are supported; score results are currently
available to Go consumers only. There is no evaluator-based model routing yet.

```yaml
evaluators:
  credential_exposure:
    provider: typesafe
    model: jev-latest
    type: boolean
    instructions: Does the operation in tool_input disclose credentials outside a trusted boundary?

agents:
  root:
    model: openai/gpt-5-mini
    instruction: Help with the project and respect tool policy decisions.
    toolsets:
      - type: shell
    hooks:
      tool_guard:
        - matcher: shell
          hooks:
            - type: evaluator
              evaluator: credential_exposure
              evaluator_policy:
                decisions:
                  "true": ask
                  "false": allow
                min_probability: 0.95
                fallback: ask
```

The guard sends only `tool_name`, `tool_input`, and `tool_category`, after input
transforms. It does not send conversation history or session identifiers.

For a choice, the policy looks up the selected outcome's probability. For a
boolean, it selects `true` when the probability is at least 0.5, otherwise `false`
with probability `1 - p`. A mapped outcome at or above `min_probability` uses its
configured decision. Unmapped or uncertain outcomes use `fallback`, which must
be `ask` or `deny`. The threshold above is illustrative: tune it on representative
and adversarial cases rather than treating it as a safety guarantee.

- **`allow` is advisory:** existing permissions and safety-mode checks still run.
- **`ask` requires fresh confirmation**, even under autonomous mode or an earlier
  “always allow” grant. Explicit denials still win. Non-interactive sessions deny.
- **`deny` blocks the call.**
- Provider errors, timeouts, or malformed answers **block**, regardless of the
  uncertainty fallback or hook `on_error` setting.

Confirmation metadata includes the evaluator name, selected outcome, probability,
and returned model. Existing [tool-guard semantics](../hooks/index.md#tool-phases-transform-guard-approve)
apply, including rechecking arguments rewritten by a later legacy hook.

## Security and limitations

This feature is opt-in and sends tool arguments to an external service, including
calls that permission rules may subsequently deny. Use trusted endpoints, minimize
input, and review redaction requirements; automatic redaction cannot detect every
secret. Redirects are disabled, and provider errors omit input and response bodies.

Jev's [documented adversarial-input limitations](https://docs.typesafe.ai/model-jaggedness/jev-1.13)
mean that an assessment is not a security boundary. A low-risk label must not
replace sandboxing, deterministic restrictions, or human review.

Embedders using strict team loading must explicitly enable `config.FeatureEvaluators`
and, for evaluator hooks, `config.FeatureHooks`.

See the runnable [evaluator tool-guard example](https://github.com/docker/docker-agent/blob/main/examples/evaluators.yaml).

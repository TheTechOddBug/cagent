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
| `cost` | Optional USD prices per million tokens: `input` and `output`. Overrides automatic pricing; `cost: {}` explicitly declares free evaluations. |

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

Results also carry the returned model ID, token usage, an optional estimated USD
`cost`, and optional provider confidence. Confidence is distinct from outcome
probability, and neither should be assumed comparable across providers. Missing
or invalid required answer fields are errors, not zero-valued assessments.

The Go API is `evaluator.Evaluator.Evaluate(ctx, state)`. State can be a string,
JSON object, or array. Loaded teams expose named clients through `Team.Evaluator`.
The initial implementation sends one question per evaluation and does not retry
failed requests automatically.

## Pricing and accounting

For the official TypeSafe endpoint, the returned model ID `jev-1.13.0` has
[documented pricing](https://docs.typesafe.ai/models) of **$0.042 per million input
tokens**, with output tokens free. Automatic pricing uses that exact returned ID,
not the requested alias: `jev-latest` is priced only if it resolves to a known
version. Unknown or future versions and custom endpoints have no assumed price.

Set an evaluator-level override for private deployments, negotiated rates, or
models without built-in pricing:

```yaml
evaluators:
  credential_exposure:
    provider: typesafe
    model: jev-1.13.0
    type: boolean
    instructions: Does this disclose credentials outside a trusted boundary?
    cost:
      input: 0.042
      output: 0
```

All prices must be finite and nonnegative. Omitted rates in a supplied `cost`
object are zero; `cost: {}` means explicitly free, not unknown. The shared cost
configuration also accepts `cache_read` and `cache_write`, but evaluators do not
currently report cached tokens, so those rates are unused. Clients snapshot their
pricing overrides when constructed.

`Result.Cost` is a `*float64` in USD: `nil` means the charge is unknown; a pointer
to zero means a known zero charge. Estimates require both valid reported token
counts and known pricing. They are not invoices: discounts, minimum charges,
unreported work, and provider billing adjustments may differ. Even free pricing
cannot establish a charge when usage is missing.

Go consumers can attach a request-scoped callback with
`evaluator.WithUsageObserver(ctx, func(evaluator.UsageRecord))`. The callback runs
synchronously once for each attempted HTTP request, including responses whose answers fail
validation or whose HTTP status is an error. Records carry the returned model ID
(or the requested ID when no usable ID is returned), `Usage *Usage`, and
`Cost *float64`. Malformed, missing, null, or incomplete usage is represented by
`Usage == nil`; explicitly reported zero input and output counts remain non-nil.
Transport failures and unreadable or malformed responses produce an unknown-usage
record. Local validation and credential failures before sending a request do not. Callbacks shared
across concurrent evaluations must synchronize their own state; a child context
replaces its inherited observer rather than adding another callback.

`Result.Usage` remains a value for compatibility, so use observation to distinguish
missing usage from explicit zeros. Observation also captures billable tokens when
`Evaluate` returns an error instead of a result. Consumers must not count the
same request again from its result.

Tool-guard evaluations are recorded as separate session items, including calls
that allow, ask, deny, or return an invalid answer. Known costs contribute to
session totals and the cost display; `/cost` includes a **By Evaluator** breakdown
and explicitly marks unknown costs. Records survive save/reload, branching, and
session export. Runtime events expose each assessment as `evaluation_usage`;
reported token counts also reach telemetry.

Evaluator input/output tokens and known costs count against run and named budgets
for the calling agent. They do not change chat context-window usage or trigger
chat compaction. Budgets are checked before an evaluation and after accounting;
a reached limit blocks the guarded tool and stops the run, never bypassing the
guard. Unknown spend produces a warning and marks cost budgets incomplete rather
than pretending the call was free.

These limits are best-effort, not billing caps: a single evaluation can cross a
limit, concurrently admitted requests can finish after it is reached, and a
provider may charge for a timed-out request without reporting usage. Such
unreported charges cannot be added to the numerical cost or token totals.

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

# Evaluators

Evaluators return typed assessments independently of chat models and tool-approval
policies. Callers decide what a probability, choice, or score means for their use
case.

Resolve a `latest.EvaluatorConfig` against the configured providers, then construct
one reusable client per team:

```go
resolved, err := cfg.Resolve(providers)
if err != nil {
    return err
}
client, err := provider.New(ctx, resolved, env)
if err != nil {
    return err
}
result, err := client.Evaluate(ctx, map[string]any{"message": message})
```

`Evaluate` accepts a string, JSON object, or JSON array. Null, numeric, boolean,
and non-JSON-serializable states are rejected before credentials are requested.
State is sent to the configured evaluator service; callers must select the data
appropriate for that service.

## TypeSafe

The initial backend posts one question named `evaluation` to `/v1/systemone`.
It requires an explicit model, uses `https://api.typesafe.ai` by default, and
retrieves `TYPESAFE_API_KEY` from the environment provider on each request. A
custom token key, base URL, and timeout can be configured. The default timeout
is 10 seconds and includes credential lookup, HTTP transfer, and response reads.
Requests respect context cancellation. Redirects are never followed, responses
are limited to 1 MiB, and errors omit credentials, input, and response bodies.
Requests are not automatically retried.

| Config type | TypeSafe question | Criteria | Result |
| --- | --- | --- | --- |
| `boolean` | `noul` | None | `Probability` in `[0, 1]` |
| `choice` | `choice` | `Choices`, a key-to-description map | `Choice` and `Probabilities` |
| `score` | `score` | `Levels`, ordered descriptions | `Score` and `Probabilities` |

Scores use zero-based level indices, with probability keys `"0"`, `"1"`, etc.
Probability maps must have exactly the configured keys and sum to one within
0.001. A selected choice must have a highest probability; ties are accepted.
Scores must lie between zero and the final level index, allowing 0.001 for
rounding. Returned values are not normalized or clamped.

`Result.Model` identifies the model returned by the service. `Result.Usage`
contains input and output token counts. Zero-valued probabilities, scores, and
confidence remain present through pointer fields. Missing or null confidence
stays `nil`; required probabilities and scores cannot be missing or null.

Run local tests with `go test -race ./pkg/evaluator/...`. Tests use fake
environment providers and local HTTP servers, not the TypeSafe API.

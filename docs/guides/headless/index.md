---
title: "Running Agents Headless & in CI"
description: "Run Docker Agent without a TUI: structured JSON output, event hooks, auto-approval strategies, and a GitHub Actions example."
keywords: docker agent, ai agents, guides, headless, ci, github actions
weight: 50
canonical: https://docs.docker.com/ai/docker-agent/guides/headless/
---

_Run Docker Agent without a TUI: structured JSON output, event hooks, auto-approval strategies, and a GitHub Actions example._

## `--exec` Mode Basics

`--exec` runs an agent without the interactive TUI: output goes to stdout and the process exits when the conversation is done. It's the mode to use in scripts, CI, and any context without a terminal.

```bash
# One-shot task, message as an argument
$ docker agent run --exec agent.yaml "Summarize the open issues in this repo"

# Pipe the message via stdin instead
$ echo "Summarize the open issues in this repo" | docker agent run --exec agent.yaml -

# Multiple messages are processed as a multi-turn conversation, in order
$ docker agent run --exec agent.yaml "question 1" "question 2" "question 3"
```

See [`docker agent run --exec`](../../features/cli/index.md#docker-agent-run---exec) for the full flag reference.

## Structured Output for Machines

Two independent things make an `--exec` run's output easy to parse: how the transcript is emitted, and what shape the model's own answer takes.

**`--json`** switches the transcript itself from human-readable text to newline-delimited JSON: one JSON object per runtime event (messages, tool calls, tool results, errors, …), instead of formatted text interleaved with tool-call boxes. Pipe it into `jq` or any NDJSON-aware log processor:

```bash
$ docker agent run --exec agent.yaml --json "List the 5 largest files in this repo" | jq -c 'select(.type == "message.delta")'
```

**`structured_output`** constrains the *model's own response* to a JSON schema you define on the agent, independent of `--json`. Use it when downstream code needs the model's answer in a predictable shape (a list of findings, a classification, …) rather than free-form prose. See [Structured Output](../../configuration/structured-output/index.md) for the full field reference — combine it with `--json` in `--exec` to get both a parseable transcript and a schema-validated final answer.

## Reacting to Events

`--on-event <type>=<cmd>` runs a shell command whenever an event of the given type fires, with the event's JSON payload piped to the command's stdin. Use `*=<cmd>` to match every event type. The flag is repeatable:

```bash
# Post a Slack notification when the agent finishes a turn
$ docker agent run --exec agent.yaml --on-event turn_end="./notify-slack.sh" "Fix the failing test"

# Log every event to a file for later inspection
$ docker agent run --exec agent.yaml --on-event "*=cat >> events.ndjson" "Fix the failing test"
```

Hooks run asynchronously and detached from the run: a hook still processing the last event when the run exits is allowed to finish, and a hook's failure is logged but never fails the run itself. This makes `--on-event` a good fit for CI notifications and logging, but not for anything CI needs to depend on for its own pass/fail result — check the agent's own exit code for that.

## Auto-Approval Strategy in CI

Interactively, the TUI prompts for confirmation before a tool call runs unless it's covered by an `allow` permission pattern. There's no one to answer that prompt in CI, so an unattended `--exec` run needs an explicit auto-approval strategy — otherwise every tool call the model attempts is rejected outright (there's no stdin to prompt, so `--exec` without one just answers "no" on your behalf; see [`--json`'s auto-reject behavior](#structured-output-for-machines) above).

You have three options, from broadest to narrowest:

- **`--yolo`** auto-approves every tool call. Simplest to set up, but it means the model can run anything its toolsets expose, unattended.
- **Permission allow-lists** (`permissions.allow` on the agent, or `settings.permissions.allow` globally) approve only specific tools or argument patterns and leave everything else to ask (which, remember, means "reject" with no one there to answer). See [Permissions](../../configuration/permissions/index.md).
- **`safe-auto` shell policy** auto-approves only commands an embedded safety classifier judges non-destructive (reads, `ls`, `git status`, …), and still asks — i.e. rejects, in `--exec` — on anything it can't classify as safe. See the `safer_shell` built-in in the [Hooks reference](../../configuration/hooks/index.md#available-built-ins).

```yaml
# Narrower than --yolo: only auto-approve read-only shell commands and MCP reads
permissions:
  allow:
    - "read_file"
    - "shell:cmd=ls*"
    - "shell:cmd=cat*"
    - "mcp:github:get_*"
    - "mcp:github:list_*"
  deny:
    - "shell:cmd=sudo*"
    - "shell:cmd=rm*"

toolsets:
  - type: shell
    safer: true # registers the safe-auto/strict safer_shell classifier
```

> [!WARNING]
> **`--yolo` in CI runs untrusted, unattended code with no one watching**
>
> A CI job is exactly the environment where a runaway or misled agent does the most damage before anyone notices — no one is at the keyboard to catch a bad `shell` call before it runs. Prefer a permission allow-list scoped to what the job actually needs over blanket `--yolo`, especially for any agent with a `shell` or `mcp` toolset that can reach production systems, secrets, or your source repository's remote.

## Providing Secrets in CI

Never put provider API keys or MCP tokens in the agent config file. Inject them as environment variables from your CI provider's secret store, or via `--env-from-file` with a file materialized at job start. See [Managing Secrets](../secrets/index.md) for every supported method, including Docker Compose secrets and 1Password references — both of which map cleanly onto CI secret stores.

## Disabling Telemetry

Docker Agent's anonymous usage telemetry is enabled by default. In CI you may want it off:

```bash
$ TELEMETRY_ENABLED=false docker agent run --exec agent.yaml "..."
```

See [Telemetry](../../community/telemetry/index.md) for exactly what is (and isn't) collected.

## Example: GitHub Actions

This job pulls an agent straight from an OCI registry reference — no checked-in config file — so there's no local `permissions:` block to edit for that agent. In this situation, a `pre_tool_use` hook is your override lever: it can allow or deny individual tool calls regardless of what the pulled config declares. This example allows read-only shell commands and denies everything else, then runs the agent non-interactively against the repository being built:

```yaml
# .github/workflows/agent-review.yml
name: Agent code review
on:
  pull_request:

jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Install docker-agent
        run: |
          curl -L "https://github.com/docker/docker-agent/releases/latest/download/docker-agent-linux-amd64" -o docker-agent
          chmod +x docker-agent
          sudo mv docker-agent /usr/local/bin/

      - name: Write the tool-approval hook
        run: |
          cat > approve-reads-only.sh <<'EOF'
          #!/bin/bash
          INPUT=$(cat)
          TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name')
          CMD=$(echo "$INPUT" | jq -r '.tool_input.cmd // empty')

          if [[ "$TOOL_NAME" == "shell" && "$CMD" =~ ^(ls|cat|grep|find|git\ (status|log|diff)) ]]; then
            echo '{"hook_specific_output": {"permission_decision": "allow"}}'
          else
            echo '{"hook_specific_output": {"permission_decision": "deny", "permission_decision_reason": "Only read-only shell commands are auto-approved in CI"}}'
          fi
          EOF
          chmod +x approve-reads-only.sh

      - name: Run the review agent
        env:
          ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
          TELEMETRY_ENABLED: "false"
        run: |
          docker-agent run --exec agentcatalog/coder --json \
            --hook-pre-tool-use "./approve-reads-only.sh" \
            "Review the changes in this PR for bugs and security issues" \
            | tee agent-events.ndjson

      - name: Upload transcript
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: agent-events
          path: agent-events.ndjson
```

See [Hooks](../../configuration/hooks/index.md#hook-output) for the full `pre_tool_use` input/output contract this script relies on. Swap the hook script, agent reference, and provider secret for your own — the shape (checkout, install the binary, run `--exec` with `--json` and an approval hook, upload the transcript) generalizes to any CI provider that can run a shell step.

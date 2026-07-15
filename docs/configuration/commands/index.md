---
title: "Custom Commands"
description: "Define slash commands that send prompts, open URLs, or switch agents, and reuse them across agents with top-level command groups."
keywords: docker agent, ai agents, configuration, yaml, custom commands, slash commands
linkTitle: "Custom Commands"
weight: 55
canonical: https://docs.docker.com/ai/docker-agent/configuration/commands/
---

_Define slash commands that send prompts, open URLs, or switch agents._

## What Slash Commands Are

A slash command is a named shortcut a user types in the TUI (`/df`, `/deploy`, `/plan`) or on the CLI (`docker agent run agent.yaml /df`) instead of typing out a full prompt. Every agent can declare its own commands under `commands:`, and top-level `commands:` groups let multiple agents share the same set without duplicating them.

Unlike regular chat messages — which are queued while the agent is busy — slash commands (both built-in and named) execute immediately, even mid-response.

Commands come in three shapes:

| Shape | What it does |
| --- | --- |
| [Prompt command](#prompt-commands) | Sends a prompt to the current agent |
| [URL command](#url-commands) | Opens a link in the user's browser (TUI only) |
| [Agent-switching command](#agent-switching-commands) | Switches the active agent, optionally with a prompt |

## Prompt Commands

The simplest form: a string value that becomes the instruction sent to the current agent.

```yaml
agents:
  root:
    model: anthropic/claude-sonnet-4-5
    description: A system administrator assistant.
    instruction: You are a system administrator.
    commands:
      df: "Check how much free space I have on my disk"
      logs: "Show me the last 50 lines of system logs"
      greet: "Say hello to ${env.USER}"
```

For more control, use the object form with an `instruction:` field, plus an optional `description:` shown in completion dialogs and help text:

```yaml
commands:
  deploy:
    description: "Deploy the application to staging"
    instruction: "Deploy ${env.PROJECT_NAME || 'app'} to ${env.ENV || 'staging'}"
```

Commands support JavaScript template literal syntax (`${env.VAR}`) for environment variable interpolation, with optional `||` defaults and ternary expressions — the same syntax as agent `instruction` and `description`. Undefined variables expand to the empty string. See [Variable Expansion in Config Fields](../overview/index.md#variable-expansion-in-config-fields) for the full picture.

Prompt commands also accept positional arguments (`$1`, `$2`, …) and bang commands (`` !`command` ``) inside `instruction`, letting a command template text typed after the slash or shell out for extra context.

```bash
# Run commands from the CLI too
$ docker agent run agent.yaml /df
$ docker agent run agent.yaml /greet
$ PROJECT_NAME=myapp ENV=production docker agent run agent.yaml /deploy
```

## URL Commands

A command with a `url` field opens that URL in the user's default browser instead of sending a prompt to the agent. Any URI scheme the OS knows how to dispatch works — standard web URLs and custom schemes such as `docker-desktop://` for deep links.

```yaml
agents:
  root:
    model: anthropic/claude-sonnet-4-5
    description: An agent with handy URL shortcuts.
    instruction: You are a helpful assistant.
    commands:
      feedback:
        description: "Open the feedback site for this session"
        url: https://example.com/feedback?session={{session_id}}
      docs:
        description: "Open the documentation"
        url: https://docs.docker.com/
      desktop:
        description: "Open this session in Docker Desktop"
        url: docker-desktop://dashboard/session/{{session_id}}
```

The `{{session_id}}` token is replaced at invocation time with the current session ID (URL-query-escaped so it can't break the URL or inject extra query parameters), letting a command deep-link to something scoped to the conversation. This token deliberately uses `{{...}}` rather than the `${...}` JS-expansion syntax, since the session ID is only known at dispatch time.

URLs are validated before being handed to the OS opener: a parseable URL with a non-empty scheme is required, and flag-like inputs (those starting with `-`) are rejected to prevent argument injection.

> [!NOTE]
> **TUI only**
>
> URL commands have no effect when run from the CLI (`docker agent run agent.yaml /docs` does nothing outside the TUI) — there's no browser to open a link in.

See [`examples/url_commands.yaml`](https://github.com/docker/docker-agent/blob/main/examples/url_commands.yaml) for a complete example.

## Agent-Switching Commands

A command with an `agent` field switches the active agent for the rest of the conversation. This is useful for building workflow shortcuts where `/plan`, `/review`, `/deploy` each route the user to the right specialist.

```yaml
agents:
  root:
    model: anthropic/claude-sonnet-4-5
    description: Main assistant
    instruction: You are a project coordinator.
    sub_agents: [planner, reviewer]
    commands:
      # Switch to planner with a pre-filled prompt
      plan:
        agent: planner
        instruction: "Create a detailed plan for: $1"
      # Switch to reviewer; any text after /review is forwarded
      review:
        agent: reviewer

  planner:
    model: anthropic/claude-sonnet-4-5
    description: Planning specialist
    instruction: You create detailed project plans.

  reviewer:
    model: anthropic/claude-sonnet-4-5
    description: Code review specialist
    instruction: You review code and suggest improvements.
```

When `agent` is set **without** `instruction`, any text typed after the slash command (e.g. `/review fix the auth bug`) is forwarded as a prompt to the target agent. When both are set, the agent is switched first, then the instruction is sent to the new agent. Either way, the target agent must be listed in the current agent's `sub_agents` array.

Agent switching stays in the same session — the target agent sees the full conversation history, and the user must explicitly switch back (there's no automatic return). This is different from the two other ways agents hand off work:

| | Agent-switching command | `handoff` tool | `transfer_task` |
| --- | --- | --- | --- |
| **Trigger** | User runs `/command` | Model calls `handoff()` | Model calls `transfer_task()` |
| **Session** | Stays in the same session | Stays in the same session | Launches an isolated sub-session |
| **History** | Target agent sees full conversation | Target agent sees full conversation | Child runs in isolation; only the result returns |
| **Control** | User must explicitly switch back | Target agent can chain to another agent | Root agent stays in control |

Use `transfer_task` (via `sub_agents`) when you want delegation with a clean result; use agent-switching commands when you want to *become* a different agent for the rest of the conversation.

See [`examples/agent_switching_commands.yaml`](https://github.com/docker/docker-agent/blob/main/examples/agent_switching_commands.yaml) for a complete example.

## Reusable Command Groups

Repeated command sets across agents can be hoisted into the top-level `commands:` section and pulled in by name with `use_commands:` — the same reuse pattern as `mcps:` for MCP servers and `toolsets:` for shared toolsets.

```yaml
commands:
  ci:
    deploy: "Deploy the application"
    test: "Run the test suite"

agents:
  root:
    model: anthropic/claude-sonnet-4-5
    description: Lead developer
    instruction: You are the lead developer. Coordinate the team.
    use_commands: [ci]      # reuse the "ci" command group
    commands:
      lint: "Run the linter"  # inline command, merged in (wins on conflict)

  docs-writer:
    model: anthropic/claude-sonnet-4-5
    description: Documentation writer
    instruction: You write and maintain the project documentation.
    use_commands: [ci]      # same group, reused without duplication
```

An agent's own inline `commands:` entries take precedence over merged `use_commands:` entries on name conflicts. See [`examples/shared-commands-skills.yaml`](https://github.com/docker/docker-agent/blob/main/examples/shared-commands-skills.yaml) for a complete example that also covers the equivalent `skills:` / `use_skills:` pattern.

## Hiding Commands

Use `--disable-commands` to hide and disable specific slash commands in the TUI — built-in ones (`/cost`, `/eval`, `/model`, …) or your own named ones. Accepts a comma-separated list; the leading slash is optional and matching is case-insensitive.

```bash
$ docker agent run agent.yaml --disable-commands="/cost,/eval,/model"
```

This is useful for shipping a distributed agent with a narrower command surface — for example, hiding `/model` so a published agent always runs its intended model.

## Built-in Commands

The TUI ships its own slash commands (`/new`, `/compact`, `/sessions`, `/settings`, …) alongside whatever an agent defines. See [Slash Commands](../../features/tui/index.md#slash-commands) in the TUI reference for the full list.

## Command Configuration Reference

| Property | Type | Description |
| --- | --- | --- |
| `description` | string | Shown in completion dialogs and help text. |
| `instruction` | string | The prompt sent to the agent. Supports bang commands (`` !`command` ``) and positional arguments (`$1`, `$2`, …). |
| `agent` | string | Name of a sub-agent to switch to when this command is invoked. Must be defined in `sub_agents`. When set without `instruction`, any text typed after the slash command is forwarded as a prompt to the target agent. |
| `url` | string | URL to open in the user's default browser when this command is invoked, instead of sending a prompt to the agent. The token `{{session_id}}` is replaced at invocation time with the current session ID (URL-query-escaped). |

`instruction` and `agent` can be combined (the agent is switched first, then the instruction is sent to the new agent). If `url` is set, it takes precedence over `agent` and `instruction` — the command only opens the browser. The simple string form is shorthand for `{ instruction: "..." }`.

---
title: "Hooks"
description: "Run shell commands at various points during agent execution for deterministic control over behavior."
permalink: /configuration/hooks/
---

# Hooks

_Run shell commands at various points during agent execution for deterministic control over behavior._

## Overview

Hooks allow you to execute shell commands or scripts at key points in an agent's lifecycle. They provide deterministic control that works alongside the LLM's behavior, enabling validation, logging, environment setup, and more.

<div class="callout callout-info" markdown="1">
<div class="callout-title">ℹ️ Use Cases
</div>

- Validate or transform tool inputs before execution
- Log all tool calls to an audit file
- Block dangerous operations based on custom rules
- Set up the environment when a session starts
- Clean up resources when a session ends
- Log or validate model responses before returning to the user
- Send external notifications on agent errors or warnings

</div>

## Hook Types

There are seven hook event types:

| Event            | When it fires                                          | Can block? |
| ---------------- | ------------------------------------------------------ | ---------- |
| `pre_tool_use`   | Before a tool call executes                            | Yes        |
| `post_tool_use`  | After a tool completes successfully                    | No         |
| `session_start`  | When a session begins or resumes                       | No         |
| `session_end`    | When a session terminates                              | No         |
| `on_user_input`  | When the agent is waiting for user input               | No         |
| `stop`           | When the model finishes responding                     | No         |
| `notification`   | When the agent emits a notification (error or warning) | No         |

## Configuration

```yaml
agents:
  root:
    model: openai/gpt-4o
    description: An agent with hooks
    instruction: You are a helpful assistant.
    hooks:
      # Run before specific tools
      pre_tool_use:
        - matcher: "shell|edit_file"
          hooks:
            - type: command
              command: "./scripts/validate-command.sh"
              timeout: 30

      # Run after all tool calls
      post_tool_use:
        - matcher: "*"
          hooks:
            - type: command
              command: "./scripts/log-tool-call.sh"

      # Run when session starts
      session_start:
        - type: command
          command: "./scripts/setup-env.sh"

      # Run when session ends
      session_end:
        - type: command
          command: "./scripts/cleanup.sh"

      # Run when agent is waiting for user input
      on_user_input:
        - type: command
          command: "./scripts/notify.sh"

      # Run when the model finishes responding
      stop:
        - type: command
          command: "./scripts/log-response.sh"

      # Run on agent errors and warnings
      notification:
        - type: command
          command: "./scripts/alert.sh"
```

## Matcher Patterns

The `matcher` field uses regex patterns to match tool names:

| Pattern            | Matches                       |
| ------------------ | ----------------------------- |
| `*`                | All tools                     |
| `shell`            | Only the `shell` tool         |
| `shell\|edit_file` | Either `shell` or `edit_file` |
| `mcp:.*`           | All MCP tools (regex)         |

## Hook Input

Hooks receive JSON input via stdin with context about the event:

```json
{
  "session_id": "abc123",
  "cwd": "/path/to/project",
  "hook_event_name": "pre_tool_use",
  "tool_name": "shell",
  "tool_use_id": "call_xyz",
  "tool_input": {
    "cmd": "rm -rf /tmp/cache",
    "cwd": "."
  }
}
```

### Input Fields by Event Type

| Field                  | pre_tool_use | post_tool_use | session_start | session_end | on_user_input | stop | notification |
| ---------------------- | ------------ | ------------- | ------------- | ----------- | ------------- | ---- | ------------ |
| `session_id`           | ✓            | ✓             | ✓             | ✓           | ✓             | ✓    | ✓            |
| `cwd`                  | ✓            | ✓             | ✓             | ✓           | ✓             | ✓    | ✓            |
| `hook_event_name`      | ✓            | ✓             | ✓             | ✓           | ✓             | ✓    | ✓            |
| `tool_name`            | ✓            | ✓             |               |             |               |      |              |
| `tool_use_id`          | ✓            | ✓             |               |             |               |      |              |
| `tool_input`           | ✓            | ✓             |               |             |               |      |              |
| `tool_response`        |              | ✓             |               |             |               |      |              |
| `source`               |              |               | ✓             |             |               |      |              |
| `reason`               |              |               |               | ✓           |               |      |              |
| `stop_response`        |              |               |               |             |               | ✓    |              |
| `notification_level`   |              |               |               |             |               |      | ✓            |
| `notification_message` |              |               |               |             |               |      | ✓            |

The `source` field for `session_start` can be: `startup`, `resume`, `clear`, or `compact`.

The `reason` field for `session_end` can be: `clear`, `logout`, `prompt_input_exit`, or `other`.

The `stop_response` field contains the model's final text response.

The `notification_level` field can be: `error` or `warning`.

## Hook Output

Hooks communicate back via JSON output to stdout:

```json
{
  "continue": true,
  "stop_reason": "Optional message when continue=false",
  "suppress_output": false,
  "system_message": "Warning message to show user",
  "decision": "allow",
  "reason": "Explanation for the decision",
  "hook_specific_output": {
    "hook_event_name": "pre_tool_use",
    "permission_decision": "allow",
    "permission_decision_reason": "Command is safe",
    "updated_input": { "cmd": "modified command" }
  }
}
```

### Output Fields

| Field             | Type    | Description                                     |
| ----------------- | ------- | ----------------------------------------------- |
| `continue`        | boolean | Whether to continue execution (default: `true`) |
| `stop_reason`     | string  | Message to show when `continue=false`           |
| `suppress_output` | boolean | Hide stdout from transcript                     |
| `system_message`  | string  | Warning message to display to user              |
| `decision`        | string  | For blocking: `block` to prevent operation      |
| `reason`          | string  | Explanation for the decision                    |

### Pre-Tool-Use Specific Output

The `hook_specific_output` for `pre_tool_use` supports:

| Field                        | Type   | Description                             |
| ---------------------------- | ------ | --------------------------------------- |
| `permission_decision`        | string | `allow`, `deny`, or `ask`               |
| `permission_decision_reason` | string | Explanation for the decision            |
| `updated_input`              | object | Modified tool input (replaces original) |

### Plain Text Output

For `session_start`, `post_tool_use`, and `stop` hooks, plain text written to stdout (i.e., output that is not valid JSON) is captured as additional context for the agent.

## Exit Codes

Hook exit codes have special meaning:

| Exit Code | Meaning                                |
| --------- | -------------------------------------- |
| `0`       | Success — continue normally            |
| `2`       | Blocking error — stop the operation    |
| Other     | Error — logged but execution continues |

## Per-hook options

Hooks have a default timeout of 60 seconds. You can also give hooks a name, add environment variables, choose a working directory, and control how non-security hook failures behave:

```yaml
hooks:
  post_tool_use:
    - matcher: "shell"
      hooks:
        - name: "summarize shell output"
          type: command
          command: "./summarize.sh"
          timeout: 120 # 2 minutes
          working_dir: ./hooks
          env:
            PROFILE: dev
          on_error: warn # warn | ignore | block
```

`pre_tool_use` is fail-closed for safety: a failed pre-tool hook blocks the tool call regardless of `on_error`.

<div class="callout callout-warning" markdown="1">
<div class="callout-title">⚠️ Performance
</div>
  <p>Hooks run synchronously and can slow down agent execution. Keep hook scripts fast and efficient. Consider using <code>suppress_output: true</code> for logging hooks to reduce noise.</p>

</div>

<div class="callout callout-info" markdown="1">
<div class="callout-title">ℹ️ Session End and Cancellation
</div>
  <p><code>session_end</code> hooks are designed to run even when the session is interrupted (e.g., Ctrl+C). They are still subject to their configured timeout.</p>

</div>

## Examples

### Validation Script

A simple pre-tool-use hook that blocks dangerous shell commands:

```bash
#!/bin/bash
# scripts/validate-command.sh

# Read JSON input from stdin
INPUT=$(cat)
TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name')
CMD=$(echo "$INPUT" | jq -r '.tool_input.cmd // empty')

# Block dangerous commands
if [[ "$TOOL_NAME" == "shell" ]]; then
  if [[ "$CMD" =~ ^sudo ]] || [[ "$CMD" =~ rm.*-rf ]]; then
    echo '{"decision": "block", "reason": "Dangerous command blocked by policy"}'
    exit 2
  fi
fi

# Allow everything else
echo '{"decision": "allow"}'
exit 0
```

### Audit Logging

A post-tool-use hook that logs all tool calls:

```bash
#!/bin/bash
# scripts/log-tool-call.sh

INPUT=$(cat)
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name')
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id')

# Append to audit log
echo "$TIMESTAMP | $SESSION_ID | $TOOL_NAME" >> ./audit.log

# Don't block execution
echo '{"continue": true}'
exit 0
```

### Session Lifecycle

Session start and end hooks for environment setup and cleanup:

```yaml
hooks:
  session_start:
    - type: command
      timeout: 10
      command: |
        INPUT=$(cat)
        SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // "unknown"')
        echo "Session $SESSION_ID started at $(date)" >> /tmp/agent-session.log
        echo '{"hook_specific_output":{"additional_context":"Session initialized."}}'

  session_end:
    - type: command
      timeout: 10
      command: |
        INPUT=$(cat)
        SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // "unknown"')
        REASON=$(echo "$INPUT" | jq -r '.reason // "unknown"')
        echo "Session $SESSION_ID ended ($REASON) at $(date)" >> /tmp/agent-session.log
```

### Response Logging with Stop Hook

Log every model response for analytics or compliance:

```yaml
hooks:
  stop:
    - type: command
      timeout: 10
      command: |
        INPUT=$(cat)
        SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // "unknown"')
        RESPONSE_LENGTH=$(echo "$INPUT" | jq -r '.stop_response // ""' | wc -c | tr -d ' ')
        echo "[$(date)] Session $SESSION_ID - Response: $RESPONSE_LENGTH chars" >> /tmp/agent-responses.log
```

The `stop` hook is useful for:

- **Response quality checks** — validate that responses meet criteria before returning
- **Analytics** — track response lengths, patterns, or content
- **Compliance logging** — record all agent outputs for audit

### Error Notifications

Send alerts when the agent encounters errors:

```yaml
hooks:
  notification:
    - type: command
      timeout: 10
      command: |
        INPUT=$(cat)
        LEVEL=$(echo "$INPUT" | jq -r '.notification_level // "unknown"')
        MESSAGE=$(echo "$INPUT" | jq -r '.notification_message // "no message"')
        echo "[$(date)] [$LEVEL] $MESSAGE" >> /tmp/agent-notifications.log
```

The `notification` hook fires when:

- The model returns an error (all models failed)
- A degenerate tool call loop is detected
- The maximum iteration limit is reached

</div>

## CLI Flags

You can add hooks from the command line without modifying the agent's YAML file. This is useful for one-off debugging, audit logging, or layering hooks onto an existing agent.

| Flag                    | Description                             |
| ----------------------- | --------------------------------------- |
| `--hook-pre-tool-use`   | Run a command before every tool call    |
| `--hook-post-tool-use`  | Run a command after every tool call     |
| `--hook-session-start`  | Run a command when a session starts     |
| `--hook-session-end`    | Run a command when a session ends       |
| `--hook-on-user-input`  | Run a command when waiting for input    |

All flags are repeatable — pass multiple to register multiple hooks.

```bash
# Add a session-start hook
$ docker agent run agent.yaml --hook-session-start "./scripts/setup-env.sh"

# Combine multiple hooks
$ docker agent run agent.yaml \
  --hook-pre-tool-use "./scripts/validate.sh" \
  --hook-post-tool-use "./scripts/log.sh"

# Add hooks to an agent from a registry
$ docker agent run agentcatalog/coder \
  --hook-pre-tool-use "./audit.sh"
```

<div class="callout callout-info" markdown="1">
<div class="callout-title">ℹ️ Merging behavior
</div>
  <p>CLI hooks are <strong>appended</strong> to any hooks already defined in the agent's YAML config. They don't replace existing hooks. Pre/post-tool-use hooks added via CLI match all tools (equivalent to <code>matcher: "*"</code>).</p>

</div>

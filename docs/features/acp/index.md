---
title: "ACP (Agent Client Protocol)"
description: "Expose Docker Agent agents via the Agent Client Protocol for integration with ACP-compatible hosts like VS Code, IDEs, and other developer tools."
keywords: docker agent, ai agents, features, acp (agent client protocol)
linkTitle: "ACP"
weight: 70
canonical: https://docs.docker.com/ai/docker-agent/features/acp/
aliases:
  - /ai/docker-agent/integrations/acp/
---

_Expose Docker Agent agents via the Agent Client Protocol for integration with ACP-compatible hosts like VS Code, IDEs, and other developer tools._

## Overview

The `docker agent serve acp` command starts an ACP server that communicates over **stdio** (standard input/output). This makes it ideal for integration with editors, IDEs, and other tools that spawn agent processes — the host sends JSON-RPC messages to Docker Agent's stdin and reads responses from stdout.

ACP is built on the [ACP Go SDK](https://github.com/coder/acp-go-sdk) and provides a standardized way for client applications to interact with AI agents.

> [!NOTE]
> **ACP vs A2A vs MCP**
>
> **ACP** connects an agent to a *host application* (IDE, CLI tool) via stdio. **A2A** connects *agents to other agents* over HTTP. **MCP** exposes agents as *tools* for other MCP clients. Choose based on your integration target.

## Usage

```bash
# Start ACP server on stdio
$ docker agent serve acp ./agent.yaml

# With a multi-agent team config
$ docker agent serve acp ./team.yaml

# From an OCI registry
$ docker agent serve acp myorg/agent:tag

# With a custom session database
$ docker agent serve acp ./agent.yaml --session-db ./my-sessions.db
```

## How It Works

1. The host application spawns `docker agent serve acp agent.yaml` as a child process
2. Communication happens over **stdin/stdout** using the ACP protocol
3. The host sends user messages, Docker Agent processes them through the agent
4. Agent responses, tool calls, and events stream back to the host
5. Sessions are persisted in a SQLite database for continuity

```bash
# Conceptual flow:
Host Application
  └── spawns: docker agent serve acp agent.yaml
        ├── stdin  ← JSON-RPC requests from host
        └── stdout → JSON-RPC responses to host
```

## Features

- **Stdio transport** — No network ports needed; ideal for subprocess integration
- **Session persistence** — SQLite-backed sessions survive process restarts
- **Agent runtime support** — Supports configured tools, multi-agent delegation, and model fallbacks. Client-supplied stdio MCP servers are supported; audio prompts are not supported; use `session/resume`, not `session/load`, for persisted sessions.
- **Multi-agent configs** — Team configurations with sub-agents work transparently
- **Filesystem operations** — Each session has its own toolsets; shell, filesystem, and Git tools resolve relative paths from that session's working directory
- **Tool permissions** — “Always allow this tool for this session” remembers approval for that tool only; it does not enable autonomous mode for other tools.

## Slash Commands

ACP advertises supported commands when a session is created or resumed, at turn start, and when agent information changes. Discovery reads command metadata only; it does not start tools or expand command instructions.

| Command | Behavior |
| --- | --- |
| `/compact [instructions]` | Runs manual compaction of the root session using the existing runtime hooks and persistence. Reports applied, skipped, or failed rather than starting an ordinary model turn. |
| `/usage` | Reports current context-token counts and the session cost snapshot without adding history or calling the model. The context limit is the last value reported for the selected agent, not a fresh provider lookup; it is unknown until a usage event supplies it and may lag a model change. Live child-session costs are not aggregated into this snapshot. |
| Configured literal-prompt commands | Replace the leading slash command with its instruction and append trailing arguments literally. Subsequent attachments are preserved. |
| Configured agent-switch commands | Switch the active agent after looking up the command in the original agent's table. A switch without text or attachments starts no model turn. The selection lasts for the active runtime; cold resume still starts its default agent. |

`compact`, `usage`, and `new` are reserved names. `/new` is not advertised or executed: clients must use `session/new` to create a fresh conversation without destroying existing history. `/usage` accepts no arguments, and both built-ins reject attachments rather than silently dropping them.

Only directly supplied leading text is parsed as a command. Text inside attached resources is never interpreted as a command. Unknown slash-prefixed messages pass through as ordinary chat. Known commands requiring URL opening, JavaScript `${...}`, or bang-tool expansion are not advertised and return an explicit error if invoked; those expansion paths are not yet integrated with ACP's permission flow.

Commands share the session's normal turn admission, cancellation, and cleanup. Manual compaction errors are fatal for that command (`compaction_failed`), even though the same diagnostic during automatic compaction can be recoverable for an ordinary prompt. A skipped compaction does not claim to have changed history. Built-in invocations and their status messages are not added to conversation history.

## Prompt Outcomes and Errors

`session/prompt` reports why the root session stopped:

| Stop reason | Meaning |
| --- | --- |
| `end_turn` | Normal completion |
| `max_tokens` | The final model response reached its output-token limit |
| `refusal` | The final model response was a refusal |
| `max_turn_requests` | Execution stopped at the iteration limit, rather than being approved to continue |
| `cancelled` | The prompt was canceled or its context expired |

Cancellation takes precedence, and responses wait until runtime events have fully drained. Child-session outcomes, recoverable compaction diagnostics, warnings, and model fallbacks do not by themselves fail the root prompt. A later successful response supersedes an earlier model stop during a continued turn.

Fatal root-runtime failures return JSON-RPC internal error `-32603` with `data.sessionId`, `data.runtimeCode`, and `data.error`. Budget termination is reported this way as `budget_exceeded`, not confused with an output-token or iteration limit. Diagnostic updates may already have streamed before the error response. Missing prompt/resume sessions return `-32002` (resource not found); invalid workspace parameters return `-32602` (invalid params).

## Client-Supplied MCP Servers

Pass stdio MCP servers in `mcpServers` on `session/new` or `session/resume`:

```json
{
  "cwd": "/home/user/project",
  "mcpServers": [
    {
      "name": "project-tools",
      "command": "/usr/local/bin/project-mcp",
      "args": ["--stdio", "--project", "/home/user/project"],
      "env": [{"name": "PROJECT_MODE", "value": "development"}]
    }
  ]
}
```

The executable path must be absolute. These are **local subprocesses**, running with the agent's OS permissions and the session's working directory. Arguments and environment values are passed literally, without shell expansion or installation. Processes inherit the agent's environment (including credentials); supplied entries override inherited values, and the last duplicate wins. Only stdio is supported here; HTTP, SSE, and MCP-over-ACP configurations are rejected.

The agent initializes the servers and lists their tools before accepting setup, within a 30-second setup budget. Client tools supplement configured tools and are exposed to the session's agents, honoring each agent's read-only filter. Model-facing names are bounded, generation-specific aliases; remembered per-tool approvals do not transfer to replacements. Existing global permission policies still apply.

Every successful resume supplies the complete replacement server list. An omitted or empty list removes all client servers. An idle session keeps its runtime and conversation; setup failure leaves its previous servers and additional roots unchanged. Cached calls to retired tools fail rather than being redirected, and calls already using the old servers are canceled and joined during retirement. Cleanup failure blocks further prompts/resumes and is retained by close/shutdown; failed cleanup remains an error for that agent process.

Servers are not automatically restarted or retried after disconnection; explicitly resume to create fresh connections. Closing a session retires and stops its client subprocesses, including while calls are pending. Connection settings and environment values are not persisted as session configuration: clients must send them again on cold resume. This does not implement an OS sandbox or full ACP elicitation support.

## Elicitation

ACP form and URL elicitation is not yet bridged to the client. Requests routed through the runtime are automatically declined instead of waiting for a response that cannot arrive. This also applies to interactive tool flows such as `user_prompt`, MCP authorization prompts, and sudo password prompts. Tools must handle the decline; operations that require the requested input may fail.

Tool-permission requests and iteration-limit continuation prompts remain interactive through `session/request_permission`. Declining elicitation does not automatically approve tools, enable autonomous mode, or stop the session from continuing with other work. Full client-capability-negotiated elicitation support is separate from this fallback.

## Session Workspaces

New sessions and resumes that reconstruct a runtime load the agent configuration again and create independent teams and toolsets. Configuration changes affect subsequent loads, not teams already serving sessions. Config-relative paths, such as instruction files, remain relative to the agent configuration file.

The session's `cwd` is the execution directory, independent of where the ACP subprocess was launched. ACP `session/new` and `session/resume` wire requests require `cwd`; the SDK rejects omitted values. Direct Go callers retain the legacy empty-`cwd` fallback: new sessions use the configured or process directory without saving invented workspace provenance, and resumes use the saved directory when available. Workspace selection does not by itself sandbox tools or make explicitly shared storage private.

A resume must name the same directory as the saved workspace. Filesystem aliases of the same directory are accepted, but the saved path is not rewritten. A legacy session with no saved workspace cannot adopt an explicit `cwd`; ACP clients must create a new session instead.

Every successful resume replaces the complete `additionalDirectories` list. Omitting it or sending an empty array revokes all additional roots; previous roots are never implicitly restored. Invalid paths or workspace mismatches leave session state unchanged.

Resuming a registered session while a foreground prompt is running, queued, or draining, or another resume holds the setup reservation, returns a busy error without canceling that work or changing roots. Retry after it finishes. This guards foreground turns, not detached background work or already-issued client I/O; it is not an atomic revocation guarantee.

## Listing Sessions

`session/list` returns up to **50 sessions per page**, ordered by creation time (newest first), then by session ID ascending to break ties. To fetch the next page, pass the returned opaque `nextCursor` as `cursor` and keep the same `cwd` filter. An absent `nextCursor` marks the end; no matches return `sessions: []`. Invalid cursors or a changed filter return invalid params (`-32602`).

The optional `cwd` filter must be an absolute path. Matching uses cleaned, exact path strings, not prefixes, case folding, or symlink resolution. The directory need not still exist, so historical workspaces remain discoverable. Sessions with unknown or non-absolute workspace metadata are omitted rather than attributed to the server's current directory.

Listings use stored summaries, not conversation histories. The optional `updatedAt` is omitted because the store does not track last activity; creation time is used only for ordering, not reported as activity. Additional roots are reported from active session state and omitted for inactive sessions.

Pagination does not freeze a snapshot across requests: changes to history or metadata between pages can affect results. Page size bounds the response, while the store still retrieves all session summaries for filtering and ordering.

## Closing Sessions

A successful `session/close` response means the foreground turn has drained, the runtime has joined its background agents, and session toolset shutdown has completed through the existing toolset lifecycle. Client MCP shutdown starts before those joins so blocked subprocess I/O can be terminated rather than strand the drain. Close also cancels and joins in-flight resumes for that session so they cannot publish a replacement runtime after closure.

Concurrent close requests join the same cleanup. Canceling a close request only stops that caller's wait: cleanup continues, and resume remains blocked until it succeeds. After a successful close, an explicit resume may reconstruct the session. Cleanup failures are returned and retained; the same session cannot reopen in that agent process when cleanup is uncertain.

Server shutdown rejects new work, cancels admitted initialization/session/list operations, and drains them before closing the session store. Shutdown is a final join rather than a bounded timeout: an uncooperative runtime or tool can delay it. Toolset stop errors are surfaced, not treated as successful cleanup. These guarantees do not add disposal support to toolsets whose resources fall outside the existing lifecycle contract, nor undo already-issued client I/O.

## Filesystem Policies and Post-Edit Hooks

The ACP `filesystem` toolset's `read_file`, `write_file`, and `edit_file` operations enforce `allow_list`, `deny_list`, and `.agentsignore` before requesting client I/O. Session workspace roots remain an additional restriction: adding a workspace root does not override a deny rule or expand the configured allow list. Invalid allow/deny configuration disables these operations.

These are path checks, not an atomic sandbox around client I/O. The ACP client must enforce access boundaries when opening files; the agent cannot apply local `os.Root` protections to another process's file operations. User-supplied `resource_link` attachments follow a separate, session-root-checked path and are not governed by a filesystem toolset's policy.

Configured `post_edit` commands run **locally**, in the session toolset's working directory, only after a successful client write. They receive the checked target path in `${file}` and match patterns against that target, not a symlink alias. Hooks require the client and agent to share a coherent on-disk filesystem; editor-buffer-only writes are not mirrored to local disk. A hook failure reports that the write succeeded but the hook failed, without retrying or rolling back the write.

## Tool Locations and File Diffs

Tool-start and permission updates report absolute file locations. Relative paths are resolved lexically against the session's known workspace, with home-directory expansion; this display metadata does not read files or grant access. URI-shaped values and relative paths without an absolute workspace are omitted.

Successful ACP `edit_file` operations can include a full-file diff captured from the client-read content and the exact content sent in the write. The completion retains its textual response as well. Snapshots preserve untouched text and newline conventions; they are not reconstructed from replacement snippets or reread when the result is displayed.

Diffs are omitted when filesystem `post_edit` commands are configured, the checked target changes between read and write, the snapshots contain invalid UTF-8, or the combined JSON-encoded diff exceeds 1 MiB. The edit still runs normally when its diff is omitted. These snapshots are not atomic filesystem history or a guarantee of final content after later runtime hooks or other writers.

`write_file` completions remain text-only: ACP offers no byte-bounded, atomic before-state read, and an optional full read could disconnect the client on a large file and prevent an otherwise valid overwrite. Unknown old content is never presented as a newly created file. Other tools without captured ACP edit metadata also remain text-only.

Captured file contents are presentation-only metadata, excluded from generic result/event JSON, model tool output, and persisted tool messages. The explicit ACP diff returns those unredacted file snapshots to the originating client; tool-response text transformations do not rewrite the diff. Raw output continues to contain only the normal transformed response text.

## CLI Flags

```bash
docker agent serve acp <agent-file>|<registry-ref> [flags]
```

See the [CLI reference](../cli/index.md#docker-agent-serve-acp) for all flags, defaults, and shared runtime options.

## Integration Example

A host application would spawn Docker Agent as a subprocess and communicate via the ACP protocol:

```javascript
// Pseudocode: request() sends newline-delimited JSON-RPC with unique IDs,
// correlates responses, and handles incoming client requests/notifications.
const client = spawnACP(["docker", "agent", "serve", "acp", "./agent.yaml"]);

await client.request("initialize", {
  protocolVersion: 1,
  clientCapabilities: {},
});
const session = await client.request("session/new", {
  cwd: process.cwd(),
  mcpServers: [],
});
await client.request("session/prompt", {
  sessionId: session.sessionId,
  prompt: [{ type: "text", text: "Explain this code" }],
});
```

> [!TIP]
> **When to use ACP**
>
> Use ACP when building **IDE integrations**, **editor plugins**, or any tool that wants to embed a Docker Agent agent as a subprocess. For HTTP-based integrations, use the [API Server](../api-server/index.md) instead.

> [!NOTE]
> **See also**
>
> For HTTP-based agent access, see the [API Server](../api-server/index.md). For agent-to-agent communication, see [A2A Protocol](../a2a/index.md). For exposing agents as MCP tools, see [MCP Mode](../mcp-mode/index.md).

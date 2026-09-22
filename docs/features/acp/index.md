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

The session's `cwd` is the execution directory, independent of where the ACP subprocess was launched. ACP wire requests require `cwd`; the SDK rejects omitted values. Direct Go callers retain the legacy empty-`cwd` fallback: new sessions use the configured or process directory without saving invented workspace provenance, and resumes use the saved directory when available. Workspace selection does not by itself sandbox tools or make explicitly shared storage private.

A resume must name the same directory as the saved workspace. Filesystem aliases of the same directory are accepted, but the saved path is not rewritten. A legacy session with no saved workspace cannot adopt an explicit `cwd`; ACP clients must create a new session instead.

Every successful resume replaces the complete `additionalDirectories` list. Omitting it or sending an empty array revokes all additional roots; previous roots are never implicitly restored. Invalid paths or workspace mismatches leave session state unchanged.

Resuming a registered session while a foreground prompt is running, queued, or draining, or another resume holds the setup reservation, returns a busy error without canceling that work or changing roots. Retry after it finishes. This guards foreground turns, not detached background work or already-issued client I/O; it is not an atomic revocation guarantee.

## Closing Sessions

A successful `session/close` response means the foreground turn has drained, the runtime has joined its background agents, and session toolset shutdown has completed through the existing toolset lifecycle. Client MCP shutdown starts before those joins so blocked subprocess I/O can be terminated rather than strand the drain. Close also cancels and joins in-flight resumes for that session so they cannot publish a replacement runtime after closure.

Concurrent close requests join the same cleanup. Canceling a close request only stops that caller's wait: cleanup continues, and resume remains blocked until it succeeds. After a successful close, an explicit resume may reconstruct the session. Cleanup failures are returned and retained; the same session cannot reopen in that agent process when cleanup is uncertain.

Server shutdown rejects new work, cancels admitted initialization/session/list operations, and drains them before closing the session store. Shutdown is a final join rather than a bounded timeout: an uncooperative runtime or tool can delay it. Toolset stop errors are surfaced, not treated as successful cleanup. These guarantees do not add disposal support to toolsets whose resources fall outside the existing lifecycle contract, nor undo already-issued client I/O.

## Filesystem Policies and Post-Edit Hooks

The ACP `filesystem` toolset's `read_file`, `write_file`, and `edit_file` operations enforce `allow_list`, `deny_list`, and `.agentsignore` before requesting client I/O. Session workspace roots remain an additional restriction: adding a workspace root does not override a deny rule or expand the configured allow list. Invalid allow/deny configuration disables these operations.

These are path checks, not an atomic sandbox around client I/O. The ACP client must enforce access boundaries when opening files; the agent cannot apply local `os.Root` protections to another process's file operations. User-supplied `resource_link` attachments follow a separate, session-root-checked path and are not governed by a filesystem toolset's policy.

Configured `post_edit` commands run **locally**, in the session toolset's working directory, only after a successful client write. They receive the checked target path in `${file}` and match patterns against that target, not a symlink alias. Hooks require the client and agent to share a coherent on-disk filesystem; editor-buffer-only writes are not mirrored to local disk. A hook failure reports that the write succeeded but the hook failed, without retrying or rolling back the write.

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

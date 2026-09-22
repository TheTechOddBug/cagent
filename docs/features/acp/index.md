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
- **Agent runtime support** — Supports configured tools, multi-agent delegation, and model fallbacks. Client-supplied MCP servers and audio prompts are not supported; use `session/resume`, not `session/load`, for persisted sessions.
- **Multi-agent configs** — Team configurations with sub-agents work transparently
- **Filesystem operations** — Each session has its own toolsets; shell, filesystem, and Git tools resolve relative paths from that session's working directory
- **Tool permissions** — “Always allow this tool for this session” remembers approval for that tool only; it does not enable autonomous mode for other tools.

## Session Workspaces

New sessions and resumes that reconstruct a runtime load the agent configuration again and create independent teams and toolsets. Configuration changes affect subsequent loads, not teams already serving sessions. Config-relative paths, such as instruction files, remain relative to the agent configuration file.

The session's `cwd` is the execution directory, independent of where the ACP subprocess was launched. ACP wire requests require `cwd`; the SDK rejects omitted values. Direct Go callers retain the legacy empty-`cwd` fallback: new sessions use the configured or process directory without saving invented workspace provenance, and resumes use the saved directory when available. Workspace selection does not by itself sandbox tools or make explicitly shared storage private.

A resume must name the same directory as the saved workspace. Filesystem aliases of the same directory are accepted, but the saved path is not rewritten. A legacy session with no saved workspace cannot adopt an explicit `cwd`; ACP clients must create a new session instead.

Every successful resume replaces the complete `additionalDirectories` list. Omitting it or sending an empty array revokes all additional roots; previous roots are never implicitly restored. Invalid paths or workspace mismatches leave session state unchanged.

Resuming a registered session while a foreground prompt is running, queued, or draining returns an error without canceling the prompt or changing roots. Retry after the prompt finishes. This guards foreground turns, not detached background work or already-issued client I/O; it is not an atomic revocation guarantee. Closing and immediately reopening a session is likewise not a synchronization barrier for its old runtime.

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

package mcp

import "github.com/docker/docker-agent/pkg/tools"

// PromptInfo and PromptArgument moved to pkg/tools so the runtime can expose
// MCP prompts without importing this package; the aliases keep existing
// callers working.

// PromptInfo is an alias for [tools.PromptInfo].
type PromptInfo = tools.PromptInfo

// PromptArgument is an alias for [tools.PromptArgument].
type PromptArgument = tools.PromptArgument

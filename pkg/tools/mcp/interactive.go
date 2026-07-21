package mcp

import (
	"context"

	"github.com/docker/docker-agent/pkg/tools"
)

// The interactive-prompt gating helpers and OAuth sentinel errors moved to
// pkg/tools so the runtime (and other embedder-facing packages) can use them
// without linking the full MCP toolset. The forwarders and aliases below keep
// this package's historical API working.

// WithoutInteractivePrompts forwards to [tools.WithoutInteractivePrompts].
func WithoutInteractivePrompts(ctx context.Context) context.Context {
	return tools.WithoutInteractivePrompts(ctx)
}

// InteractivePromptsAllowed forwards to [tools.InteractivePromptsAllowed].
func InteractivePromptsAllowed(ctx context.Context) bool {
	return tools.InteractivePromptsAllowed(ctx)
}

// AuthorizationRequiredError is an alias for [tools.AuthorizationRequiredError].
type AuthorizationRequiredError = tools.AuthorizationRequiredError

// IsAuthorizationRequired forwards to [tools.IsAuthorizationRequired].
func IsAuthorizationRequired(err error) bool {
	return tools.IsAuthorizationRequired(err)
}

// OAuthDeclinedError is an alias for [tools.OAuthDeclinedError].
type OAuthDeclinedError = tools.OAuthDeclinedError

// IsOAuthDeclined forwards to [tools.IsOAuthDeclined].
func IsOAuthDeclined(err error) bool {
	return tools.IsOAuthDeclined(err)
}

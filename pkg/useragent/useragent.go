// Package useragent centralizes the HTTP identity headers that built-in
// tools (api, fetch, openapi, ...) attach to every outbound request.
//
// All built-in tools call [SetIdentity] before sending the HTTP request so
// the wire format is uniform across tool kinds and easy to evolve from a
// single place.
package useragent

import (
	"context"
	"fmt"
	"net/http"
	"runtime"

	"github.com/docker/docker-agent/pkg/desktop"
	"github.com/docker/docker-agent/pkg/version"
)

// Header is the User-Agent value sent by built-in HTTP tools. It also
// doubles as the agent name fed to robotstxt.RobotsData.TestAgent so
// site operators see the same identity in both places.
var Header = fmt.Sprintf("Cagent/%s (%s; %s)", version.Version, runtime.GOOS, runtime.GOARCH)

// HTTP header names emitted by [SetIdentity] in addition to User-Agent.
// They surface the docker-agent and Docker Desktop versions so backends
// can correlate built-in tool calls with the rest of Docker's traffic
// for the same install.
const (
	HeaderAgentVersion   = "X-Docker-Agent-Version"
	HeaderDesktopVersion = "X-Docker-Desktop-Version"
)

// SetIdentity stamps the outgoing request with a consistent set of
// identity headers:
//
//   - User-Agent: see [Header].
//   - X-Docker-Agent-Version: the docker-agent version (always sent —
//     the literal "dev" used in dev builds is still useful to
//     disambiguate from non-cagent traffic).
//   - X-Docker-Desktop-Version: the running Docker Desktop version, only
//     when Desktop is reachable on this machine. Looked up once per
//     process via [desktop.GetVersion]; absent on hosts that don't run
//     Desktop, so its presence is itself a signal.
//
// SetIdentity uses [http.Header.Set] semantics, so callers that want to
// honour an operator-supplied override should apply those headers AFTER
// calling SetIdentity. This matches the precedence rule already used in
// the fetch tool for User-Agent and Accept.
func SetIdentity(ctx context.Context, req *http.Request) {
	req.Header.Set("User-Agent", Header)
	req.Header.Set(HeaderAgentVersion, version.Version)
	if v := desktop.GetVersion(ctx); v != "" {
		req.Header.Set(HeaderDesktopVersion, v)
	}
}

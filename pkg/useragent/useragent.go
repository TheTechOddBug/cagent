// Package useragent centralizes the HTTP identity headers that built-in
// tools (api, fetch, openapi, ...) attach to every outbound request.
package useragent

import (
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
const (
	HeaderAgentVersion   = "X-Docker-Agent-Version"
	HeaderDesktopVersion = "X-Docker-Desktop-Version"
)

// SetIdentity stamps the request with User-Agent, X-Docker-Agent-Version,
// and (when Docker Desktop is reachable) X-Docker-Desktop-Version. Callers
// that want operator-supplied overrides should apply those headers AFTER
// calling SetIdentity.
func SetIdentity(req *http.Request) {
	req.Header.Set("User-Agent", Header)
	req.Header.Set(HeaderAgentVersion, version.Version)
	if v := desktop.GetVersion(); v != "" {
		req.Header.Set(HeaderDesktopVersion, v)
	}
}

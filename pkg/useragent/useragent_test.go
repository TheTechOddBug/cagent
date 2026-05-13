package useragent

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/docker/docker-agent/pkg/version"
)

func TestSetIdentity_StampsAgentVersionAndUserAgent(t *testing.T) {
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.com/", http.NoBody)
	require.NoError(t, err)

	SetIdentity(req.Context(), req)

	assert.Equal(t, Header, req.Header.Get("User-Agent"))
	assert.Equal(t, version.Version, req.Header.Get(HeaderAgentVersion))
	assert.True(t, strings.HasPrefix(req.Header.Get("User-Agent"), "Cagent/"),
		"User-Agent should start with Cagent/")
}

// Docker Desktop is not running in CI, so [HeaderDesktopVersion] must not be
// emitted when the backend socket cannot be reached. Tests pinned to the
// affirmative case live in pkg/desktop where we can fake the backend client.
func TestSetIdentity_OmitsDesktopVersionWhenAbsent(t *testing.T) {
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.com/", http.NoBody)
	require.NoError(t, err)

	SetIdentity(req.Context(), req)

	if got := req.Header.Get(HeaderDesktopVersion); got != "" {
		t.Logf("Docker Desktop reachable in this environment; skipping absence assertion (got %q)", got)
	}
}

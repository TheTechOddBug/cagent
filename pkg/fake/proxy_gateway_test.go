package fake

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
)

func TestGatewayTargetURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		gateway string
		path    string
		want    string
	}{
		{
			name:    "root gateway",
			gateway: "https://gateway.example.com",
			path:    "/v1/messages",
			want:    "https://gateway.example.com/v1/messages",
		},
		{
			name:    "gateway with trailing slash",
			gateway: "https://gateway.example.com/",
			path:    "/v1/messages",
			want:    "https://gateway.example.com/v1/messages",
		},
		{
			name:    "gateway with path prefix",
			gateway: "https://api.docker.com/models",
			path:    "/v1/chat/completions",
			want:    "https://api.docker.com/models/v1/chat/completions",
		},
		{
			name:    "merges gateway and request query",
			gateway: "https://api.docker.com/models?tier=pro",
			path:    "/v1/chat/completions?stream=true",
			want:    "https://api.docker.com/models/v1/chat/completions?stream=true&tier=pro",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, tt.path, http.NoBody)
			got, err := GatewayTargetURL(tt.gateway, req)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGatewayAuthHeaderUpdater_NonDockerGateway(t *testing.T) {
	updater := gatewayAuthHeaderUpdater("https://gateway.example.com")

	t.Run("no env key keeps client headers", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "")

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://gateway.example.com/v1/messages", http.NoBody)
		require.NoError(t, err)
		req.Header.Set("X-Api-Key", "client-key")

		updater("https://api.anthropic.com", req)

		assert.Equal(t, "client-key", req.Header.Get("X-Api-Key"))
	})

	t.Run("env key replaces the Desktop token the client attached", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "env-key")

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://gateway.example.com/v1/messages", http.NoBody)
		require.NoError(t, err)
		req.Header.Set("X-Api-Key", "desktop-jwt")
		req.Header.Set("Authorization", "Bearer desktop-jwt")

		updater("https://api.anthropic.com", req)

		assert.Equal(t, "env-key", req.Header.Get("X-Api-Key"))
		assert.Empty(t, req.Header.Get("Authorization"))
	})
}

// Recording through an upstream gateway must forward the request (with the
// client's auth headers) to the gateway, not the provider's public endpoint,
// and record the interaction under the canonical provider URL so the cassette
// stays replayable.
func TestStartRecordingProxy_UpstreamGateway(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	var gotPath, gotAPIKey, gotForward string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("X-Api-Key")
		gotForward = r.Header.Get("X-Cagent-Forward")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer gateway.Close()

	cassettePath := t.TempDir() + "/recording"
	// gatewayAuthHeaderUpdater for a non-Docker gateway is a no-op; passed
	// explicitly because the httptest gateway is localhost, which the Docker
	// token trust check would otherwise match.
	proxyURL, cleanup, err := StartStreamingRecordingProxy(t.Context(), cassettePath, gateway.URL,
		gatewayAuthHeaderUpdater("https://gateway.example.com"))
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, proxyURL+"/v1/messages", strings.NewReader(`{"model":"claude"}`))
	require.NoError(t, err)
	req.Header.Set("X-Cagent-Forward", "https://api.anthropic.com")
	req.Header.Set("X-Api-Key", "gateway-key")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `{"ok":true}`, string(body))
	assert.Equal(t, "/v1/messages", gotPath)
	assert.Equal(t, "gateway-key", gotAPIKey, "gateway auth must reach the upstream gateway")
	assert.Equal(t, "https://api.anthropic.com", gotForward, "forward header must reach the upstream gateway")

	require.NoError(t, cleanup())

	data, err := os.ReadFile(cassettePath + ".yaml")
	require.NoError(t, err)
	c, err := cassette.Load(cassettePath)
	require.NoError(t, err)
	require.Len(t, c.Interactions, 1)
	assert.Equal(t, "https://api.anthropic.com/v1/messages", c.Interactions[0].Request.URL,
		"cassette must record the canonical provider URL, not the gateway URL")
	assert.NotContains(t, string(data), "gateway-key", "auth must not leak into the cassette")
}

// Without an upstream gateway the proxy keeps its historical behavior:
// forward to the provider's public endpoint with env-provided API keys.
func TestStartRecordingProxy_NoGatewayRejectsUnknownHost(t *testing.T) {
	cassettePath := t.TempDir() + "/recording"
	proxyURL, cleanup, err := StartRecordingProxy(t.Context(), cassettePath, "")
	require.NoError(t, err)
	defer func() { require.NoError(t, cleanup()) }()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, proxyURL+"/v1/messages", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("X-Cagent-Forward", "https://unknown.example.com")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

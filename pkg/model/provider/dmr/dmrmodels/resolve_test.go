package dmrmodels

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDMRFallbackURLs(t *testing.T) {
	t.Parallel()

	t.Run("inside container", func(t *testing.T) {
		t.Parallel()

		urls := getDMRFallbackURLs(true)

		// Should return 3 container-specific fallback URLs
		require.Len(t, urls, 3)

		// Verify the expected URLs in order (container-specific endpoints)
		assert.Equal(t, "http://model-runner.docker.internal/engines/v1/", urls[0])
		assert.Equal(t, "http://host.docker.internal:12434/engines/v1/", urls[1])
		assert.Equal(t, "http://172.17.0.1:12434/engines/v1/", urls[2])
	})

	t.Run("on host", func(t *testing.T) {
		t.Parallel()

		urls := getDMRFallbackURLs(false)

		// Should return 1 host-specific fallback URL
		require.Len(t, urls, 1)

		// Verify localhost is the only fallback on host
		assert.Equal(t, "http://127.0.0.1:12434/engines/v1/", urls[0])
	})
}

func TestDMRConnectivity(t *testing.T) {
	t.Parallel()

	t.Run("reachable endpoint", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/models", r.URL.Path)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":[]}`))
		}))
		defer server.Close()

		result := testDMRConnectivity(t.Context(), server.Client(), server.URL+"/")
		assert.True(t, result)
	})

	t.Run("reachable endpoint with error response", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		// Should still return true because server is reachable
		result := testDMRConnectivity(t.Context(), server.Client(), server.URL+"/")
		assert.True(t, result)
	})

	t.Run("unreachable endpoint", func(t *testing.T) {
		t.Parallel()

		// Use a port that's unlikely to have anything listening
		result := testDMRConnectivity(t.Context(), &http.Client{}, "http://127.0.0.1:59999/")
		assert.False(t, result)
	})
}

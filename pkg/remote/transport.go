package remote

import (
	"context"
	"net/http"

	"github.com/docker/docker-agent/pkg/desktop/transport"
)

// NewTransport returns an HTTP transport that uses the Docker Desktop proxy
// if available, and falls back to direct connections.
//
// Deprecated: use [transport.New] instead; it lives in a leaf package that
// does not pull the OCI registry dependencies of this package.
func NewTransport(ctx context.Context) http.RoundTripper {
	return transport.New(ctx)
}

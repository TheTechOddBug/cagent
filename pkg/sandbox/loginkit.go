package sandbox

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker-agent/pkg/paths"
)

// LoginKit materialises a tiny sbx mixin kit that makes the sandbox
// proxy authenticate gateway requests with the user's Docker login:
// the kit declares the reserved `sbx-login` credential service, which
// the proxy resolves to a fresh `docker login` / `sbx login` bearer
// JWT at request time and injects as `Authorization: Bearer <jwt>` on
// HTTPS requests to the gateway host. The kit also exports
// DOCKER_TOKEN as a proxy-managed sentinel inside the sandbox so the
// in-sandbox docker-agent's "signed in to Docker Desktop?" preflight
// checks pass; the sentinel never reaches the gateway (the proxy
// strips it and injects the real token).
//
// This replaces the old hack of periodically writing the short-lived
// JWT to a sandbox-tokens.json file bind-mounted into the sandbox.
//
// The kit is written to a deterministic per-host directory under the
// cache dir, so its content doubles as a reuse marker: callers mount
// the directory read-only, which forces sandboxes created before the
// kit existed (or for another gateway host) to be recreated with the
// injection configured.
//
// Returns "" when gateway is empty or does not target a docker.com
// host over HTTPS — the proxy only ever injects the login token into
// docker.com / *.docker.com hosts, so any other gateway authenticates
// by its own means (API keys, URL credentials, ...).
func LoginKit(gateway string) (string, error) {
	host := dockerGatewayHostname(gateway)
	if host == "" {
		return "", nil
	}

	dir := filepath.Join(paths.GetCacheDir(), "sandbox-login-kit", host)
	// Only the host-side sandbox CLI reads the kit (at create time, as
	// the same user); nothing inside the sandbox needs it, so keep it
	// owner-only.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating login kit directory: %w", err)
	}

	spec := fmt.Sprintf(`schemaVersion: "2"
kind: mixin
name: docker-agent-gateway-auth
description: Authenticate Docker AI gateway requests with the user's Docker login

credentials:
  - service: sbx-login
    apiKey:
      name: DOCKER_TOKEN
      proxyManaged: true
      inject:
        - domain: %s
          header: Authorization
          format: "Bearer %%s"
`, host)

	if err := os.WriteFile(filepath.Join(dir, "spec.yaml"), []byte(spec), 0o600); err != nil {
		return "", fmt.Errorf("writing login kit spec: %w", err)
	}
	return dir, nil
}

// dockerGatewayHostname returns the hostname of gateway when it is an
// HTTPS URL on docker.com or a subdomain, and "" otherwise.
func dockerGatewayHostname(gateway string) string {
	if gateway == "" {
		return ""
	}
	u, err := url.Parse(gateway)
	if err != nil || u.Scheme != "https" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "docker.com" || strings.HasSuffix(host, ".docker.com") {
		return host
	}
	return ""
}

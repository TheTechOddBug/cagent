package sandbox_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/docker/docker-agent/pkg/paths"
	"github.com/docker/docker-agent/pkg/sandbox"
)

func TestLoginKit_DockerGateway(t *testing.T) {
	cacheDir := t.TempDir()
	paths.SetCacheDir(cacheDir)
	t.Cleanup(func() { paths.SetCacheDir("") })

	dir, err := sandbox.LoginKit("https://api.docker.com/v1/gateway?x=1")
	require.NoError(t, err)
	require.NotEmpty(t, dir)
	assert.Equal(t, filepath.Join(cacheDir, "sandbox-login-kit", "api.docker.com"), dir)

	spec, err := os.ReadFile(filepath.Join(dir, "spec.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(spec), "service: sbx-login")
	assert.Contains(t, string(spec), "name: DOCKER_TOKEN")
	assert.Contains(t, string(spec), "proxyManaged: true")
	assert.Contains(t, string(spec), "domain: api.docker.com")
	assert.Contains(t, string(spec), `format: "Bearer %s"`)
	assert.NotContains(t, string(spec), "%!", "fmt verbs must not leak into the YAML")
}

func TestLoginKit_HostIsLowercasedAndPortStripped(t *testing.T) {
	cacheDir := t.TempDir()
	paths.SetCacheDir(cacheDir)
	t.Cleanup(func() { paths.SetCacheDir("") })

	dir, err := sandbox.LoginKit("https://Gateway.Docker.com:443/proxy")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cacheDir, "sandbox-login-kit", "gateway.docker.com"), dir)
}

func TestLoginKit_NonDockerGateways(t *testing.T) {
	t.Parallel()

	// The sandbox proxy only ever injects the login token into
	// docker.com hosts over HTTPS; any other gateway must not get a kit.
	for _, gateway := range []string{
		"",
		"https://models.example.com",
		"http://api.docker.com",           // plaintext
		"https://notdocker.com",           // suffix trickery
		"https://docker.com.evil.example", // prefix trickery
		"http://localhost:8080",
		"::bogus::",
	} {
		dir, err := sandbox.LoginKit(gateway)
		require.NoError(t, err, "gateway %q", gateway)
		assert.Empty(t, dir, "gateway %q must not produce a login kit", gateway)
	}
}

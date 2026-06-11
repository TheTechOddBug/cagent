package environment_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/docker/docker-agent/pkg/environment"
)

func TestIsDockerURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url      string
		expected bool
	}{
		// Valid Docker URLs
		{"https://docker.com/some/path", true},
		{"https://desktop.docker.com/mcp/catalog/v3/catalog.yaml", true},
		{"https://api.docker.com/events/v1/track", true},
		{"https://api-stage.docker.com/events/v1/track", true},
		{"https://hub.docker.com/mcp/server", true},
		{"https://sub.sub.docker.com/path", true},
		{"http://docker.com/path", true},

		// Non-Docker URLs
		{"https://example.com/agent.yaml", false},
		{"https://github.com/docker/repo", false},
		{"http://localhost:8080/agent.yaml", false},
		{"", false},

		// Security: malicious URLs that should NOT be treated as Docker URLs
		{"https://evil.com/docker.com/file.yaml", false},     // docker.com in path
		{"https://notdocker.com/file.yaml", false},           // similar domain name
		{"https://docker.com.attacker.com/file.yaml", false}, // docker.com as subdomain of attacker
		{"https://fakedocker.com/agent.yaml", false},         // contains "docker.com" substring
		{"https://attacker.com?redirect=docker.com", false},  // docker.com in query string
		{"https://my-docker.com/agent.yaml", false},          // hyphenated similar domain
		{"https://xdocker.com/agent.yaml", false},            // prefixed similar domain
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, environment.IsDockerURL(tt.url))
		})
	}
}

package environment

import (
	"context"
	"net/url"
	"strings"

	"github.com/docker/docker-agent/pkg/desktop"
)

const (
	DockerDesktopEmail    = "DOCKER_EMAIL"
	DockerDesktopUsername = "DOCKER_USERNAME"
	DockerDesktopTokenEnv = "DOCKER_TOKEN"
)

// IsDockerURL checks if the URL targets a .docker.com domain that should
// receive the Docker Desktop JWT. The check matches the exact host
// "docker.com" as well as any subdomain (e.g. "desktop.docker.com").
// It performs strict hostname validation to prevent token leakage.
func IsDockerURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "docker.com" || strings.HasSuffix(host, ".docker.com")
}

type DockerDesktopProvider struct{}

func NewDockerDesktopProvider() *DockerDesktopProvider {
	return &DockerDesktopProvider{}
}

func (p *DockerDesktopProvider) Get(ctx context.Context, name string) (string, bool) {
	switch name {
	case DockerDesktopEmail:
		return desktop.GetUserInfo(ctx).Email, true

	case DockerDesktopUsername:
		return desktop.GetUserInfo(ctx).Username, true

	case DockerDesktopTokenEnv:
		return desktop.GetToken(ctx), true

	default:
		return "", false
	}
}

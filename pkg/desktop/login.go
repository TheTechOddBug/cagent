package desktop

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type DockerHubInfo struct {
	Username string `json:"id"`
	Email    string `json:"email,omitempty"`
}

// GetToken returns Docker Desktop's access token. Desktop's newer auth stack
// (auth v2) serves whatever its in-memory token source holds and never
// refreshes on GET, so a stuck background refresher makes it return the same
// expired JWT forever. When that happens we force a refresh on Desktop's side.
func GetToken(ctx context.Context) string {
	token := fetchToken(ctx)
	if token == "" || !tokenExpired(token) {
		return token
	}

	if fresh := forceTokenRefresh(ctx); fresh != "" {
		return fresh
	}
	return token
}

func GetUserInfo(ctx context.Context) DockerHubInfo {
	var info DockerHubInfo
	_ = ClientBackend.Get(ctx, "/registry/info", &info)
	return info
}

func fetchToken(ctx context.Context) string {
	var token string
	_ = ClientBackend.Get(ctx, "/registry/token", &token)
	return token
}

// tokenExpired reports whether the JWT's exp claim is in the past.
// Tokens that don't parse or carry no exp claim are treated as valid.
func tokenExpired(token string) bool {
	parsed, _, err := jwt.NewParser().ParseUnverified(token, jwt.MapClaims{})
	if err != nil {
		return false
	}
	exp, err := parsed.Claims.GetExpirationTime()
	if err != nil || exp == nil {
		return false
	}
	return exp.Before(time.Now())
}

var refreshState struct {
	sync.Mutex

	lastAttempt time.Time
}

// Vars, not consts, so tests can shorten them.
var (
	refreshCooldown     = 30 * time.Second
	refreshPollBudget   = 10 * time.Second
	refreshPollInterval = 500 * time.Millisecond
)

// forceTokenRefresh nudges Docker Desktop to reload its session from the OS
// credential store and refresh it — the same hook the Docker CLI triggers
// after `docker login`. Desktop handles it asynchronously, so we poll until a
// non-expired token shows up. Returns "" if no fresh token was obtained.
func forceTokenRefresh(ctx context.Context) string {
	refreshState.Lock()
	defer refreshState.Unlock()

	if time.Since(refreshState.lastAttempt) < refreshCooldown {
		// A concurrent caller may have just refreshed; re-check once
		// instead of nudging Desktop again.
		if token := fetchToken(ctx); token != "" && !tokenExpired(token) {
			return token
		}
		return ""
	}
	refreshState.lastAttempt = time.Now()

	slog.DebugContext(ctx, "Docker Desktop returned an expired token, forcing a refresh")
	if err := ClientBackend.Post(ctx, "/registry/credstore-updated"); err != nil {
		slog.DebugContext(ctx, "Failed to trigger Docker Desktop token refresh", "error", err)
		return ""
	}

	deadline := time.After(refreshPollBudget)
	ticker := time.NewTicker(refreshPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ""
		case <-deadline:
			slog.DebugContext(ctx, "Docker Desktop did not deliver a fresh token in time")
			return ""
		case <-ticker.C:
			if token := fetchToken(ctx); token != "" && !tokenExpired(token) {
				return token
			}
		}
	}
}

package desktop

import (
	"context"
	"time"

	"github.com/kofalt/go-memoize"
)

// versionMemoizer caches the Docker Desktop version lookup with a TTL so:
//   - if docker-agent starts before Desktop is ready, version detection
//     recovers automatically once Desktop comes up;
//   - if Desktop is upgraded mid-session, the new version is picked up
//     within at most one TTL.
var versionMemoizer = memoize.NewMemoizer(5*time.Minute, 10*time.Minute)

// GetVersion returns the running Docker Desktop version (e.g. "4.74.0") or
// an empty string if Docker Desktop is not running or the call fails.
//
// The lookup is bounded by a short internal timeout so a stale or missing
// backend socket cannot stall callers on hot paths (it is queried on every
// outbound built-in tool HTTP request). The HTTP call uses
// [context.Background] rather than a caller-supplied context: the result
// is shared across all callers, so we don't want the first caller's
// deadline or cancellation to bleed into other callers' view of the world.
func GetVersion() string {
	v, _, _ := memoize.Call(versionMemoizer, "desktopVersion", func() (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		var info struct {
			CurrentVersion string `json:"currentVersion"`
		}
		_ = ClientBackend.Get(ctx, "/update", &info)
		return info.CurrentVersion, nil
	})
	return v
}

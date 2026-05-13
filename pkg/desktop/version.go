package desktop

import (
	"context"
	"time"

	"github.com/kofalt/go-memoize"
	"github.com/patrickmn/go-cache"
)

// updateInfo is the subset of Docker Desktop's `/update` endpoint payload
// we care about. The endpoint exposes the running app's version and build
// number; the full body also contains appcast and update-status fields
// that are irrelevant here.
type updateInfo struct {
	CurrentVersion string `json:"currentVersion"`
	CurrentBuild   string `json:"currentBuild"`
}

var versionMemoizer = memoize.NewMemoizer(cache.NoExpiration, cache.NoExpiration)

// GetVersion returns the running Docker Desktop version (e.g. "4.74.0") or
// an empty string if Docker Desktop is not running or the call fails.
//
// The lookup is memoized for the lifetime of the process — Desktop's
// version cannot change without a restart that would also tear down our
// process — and is bounded by a short internal timeout so a stale or
// missing backend socket cannot stall callers on hot paths (it is
// queried on every outbound built-in tool HTTP request).
func GetVersion(ctx context.Context) string {
	v, _, _ := versionMemoizer.Memoize("desktopVersion", func() (any, error) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()

		var info updateInfo
		_ = ClientBackend.Get(ctx, "/update", &info)
		return info.CurrentVersion, nil
	})
	return v.(string)
}

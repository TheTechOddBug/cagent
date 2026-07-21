package dmr

import (
	"context"

	"github.com/docker/docker-agent/pkg/model/provider/dmr/dmrmodels"
)

// ListModels returns the IDs of the models available to Docker Model Runner.
// It is a thin wrapper around [dmrmodels.ListModels], kept here for
// convenience of callers that already import the full DMR provider; callers
// that only need model discovery should import the lighter dmrmodels package
// instead.
func ListModels(ctx context.Context) ([]string, error) {
	return dmrmodels.ListModels(ctx)
}

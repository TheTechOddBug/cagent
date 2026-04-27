package builtins

import (
	"context"
	"time"

	"github.com/docker/docker-agent/pkg/hooks"
)

// addDate emits today's date as turn_start additional context.
func addDate(_ context.Context, _ *hooks.Input, _ []string) (*hooks.Output, error) {
	return turnStartContext("Today's date: " + time.Now().Format("2006-01-02")), nil
}

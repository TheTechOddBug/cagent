package builtins

import (
	"context"
	"fmt"
	"runtime"

	"github.com/docker/docker-agent/pkg/hooks"
)

// AddEnvironmentInfo is the registered name of the add_environment_info builtin.
const AddEnvironmentInfo = "add_environment_info"

// addEnvironmentInfo emits cwd / git / OS / arch info as session_start
// additional context. No-op when Cwd is empty.
func addEnvironmentInfo(_ context.Context, in *hooks.Input, _ []string) (*hooks.Output, error) {
	if in == nil || in.Cwd == "" {
		return nil, nil
	}
	return hooks.NewAdditionalContextOutput(hooks.EventSessionStart, getEnvironmentInfo(in.Cwd)), nil
}

// getEnvironmentInfo returns formatted environment information including
// working directory, git repository status, and platform information.
func getEnvironmentInfo(workingDir string) string {
	return fmt.Sprintf(`Here is useful information about the environment you are running in:
	<env>
	Working directory: %s
	Is directory a git repo: %s
	Operating System: %s
	CPU Architecture: %s
	</env>`, workingDir, boolToYesNo(isGitRepo(workingDir)), getOperatingSystem(), getArchitecture())
}

// boolToYesNo converts a boolean to "Yes" or "No" string.
func boolToYesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

func getOperatingSystem() string {
	switch runtime.GOOS {
	case "darwin":
		return "MacOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	default:
		return runtime.GOOS
	}
}

func getArchitecture() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}

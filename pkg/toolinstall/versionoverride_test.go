package toolinstall

import (
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvalVersionConstraint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		constraint string
		version    string
		want       bool
	}{
		{"empty matches", "", "v1.0.0", true},
		{"literal true", "true", "v1.0.0", true},
		{"literal false", "false", "v1.0.0", false},
		{"semver in range", `semver("<= 0.25.1")`, "v0.20.0", true},
		{"semver out of range", `semver("<= 0.25.1")`, "v0.30.0", false},
		{"semver with v prefix", `semver(">= 1.0.0")`, "v1.2.3", true},
		{"equality match", `Version == "0.26.0"`, "0.26.0", true},
		{"equality no match", `Version == "0.26.0"`, "0.27.0", false},
		{"startsWith", `Version startsWith "v1."`, "v1.4.0", true},
		{"in list", `Version in ["v1.0.0", "v1.1.0"]`, "v1.1.0", true},
		{"or expression", `Version == "1.0.0" or semver(">= 2.0.0")`, "v2.5.0", true},
		{"caret range excludes next major", `semver("^1.2.0")`, "v2.0.0", false},
		{"invalid constraint is no match", `this is not valid (`, "v1.0.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, evalVersionConstraint(tt.constraint, tt.version))
		})
	}
}

func TestSemverSatisfies(t *testing.T) {
	t.Parallel()

	assert.True(t, semverSatisfies("v1.2.3", ">= 1.0.0"))
	assert.True(t, semverSatisfies("1.2", "< 2.0.0")) // coerced to 1.2.0
	assert.False(t, semverSatisfies("v2.0.0", "<= 1.5.0"))
	// Pre-releases are excluded from plain ranges (npm/cargo semantics).
	assert.False(t, semverSatisfies("v1.0.0-rc1", ">= 0.1.0"))
	assert.False(t, semverSatisfies("not-a-version", ">= 0.1.0"))
	assert.False(t, semverSatisfies("v1.0.0", "not-a-constraint ("))
}

// fzfRegistry mirrors the real aqua junegunn/fzf shape: an empty base with a
// "false" constraint and version_overrides whose asset/format/checksum change
// across versions, ending in a "true" catch-all for the newest releases.
const fzfRegistryYAML = `
type: github_release
repo_owner: junegunn
repo_name: fzf
version_constraint: "false"
version_overrides:
  - version_constraint: semver("<= 0.25.1")
    asset: fzf-{{.Version}}-{{.OS}}_{{.Arch}}.{{.Format}}
    format: tar.gz
    checksum:
      type: github_release
      asset: fzf_{{.Version}}_checksums.txt
      algorithm: sha256
    overrides:
      - goos: windows
        format: zip
  - version_constraint: "true"
    asset: fzf-{{trimV .Version}}-{{.OS}}_{{.Arch}}.{{.Format}}
    format: tar.gz
    overrides:
      - goos: windows
        format: zip
    checksum:
      type: github_release
      asset: fzf_{{trimV .Version}}_checksums.txt
      algorithm: sha256
`

func parsePackage(t *testing.T, y string) *Package {
	t.Helper()
	var pkg Package
	require.NoError(t, yaml.Unmarshal([]byte(y), &pkg))
	return &pkg
}

func TestResolveVersionOverride_FZF_Newest(t *testing.T) {
	t.Parallel()

	pkg := parsePackage(t, fzfRegistryYAML)
	resolved := resolveVersionOverride(pkg, "v0.65.0")

	// The "true" catch-all override supplies the asset/checksum.
	assert.Equal(t, "fzf-{{trimV .Version}}-{{.OS}}_{{.Arch}}.{{.Format}}", resolved.Asset)
	assert.Equal(t, "tar.gz", resolved.Format)
	require.NotNil(t, resolved.Checksum)
	assert.Equal(t, "fzf_{{trimV .Version}}_checksums.txt", resolved.Checksum.Asset)
	assert.Equal(t, "sha256", resolved.Checksum.Algorithm)
	// Base identity is preserved.
	assert.Equal(t, "junegunn", resolved.RepoOwner)
	assert.Equal(t, "github_release", resolved.Type)
}

func TestResolveVersionOverride_FZF_Old(t *testing.T) {
	t.Parallel()

	pkg := parsePackage(t, fzfRegistryYAML)
	resolved := resolveVersionOverride(pkg, "v0.20.0")

	// The "<= 0.25.1" override wins; its asset template lacks trimV.
	assert.Equal(t, "fzf-{{.Version}}-{{.OS}}_{{.Arch}}.{{.Format}}", resolved.Asset)
	require.NotNil(t, resolved.Checksum)
	assert.Equal(t, "fzf_{{.Version}}_checksums.txt", resolved.Checksum.Asset)
}

func TestResolveVersionOverride_NoOverrides(t *testing.T) {
	t.Parallel()

	pkg := &Package{RepoOwner: "acme", RepoName: "tool", Asset: "tool.tar.gz"}
	assert.Same(t, pkg, resolveVersionOverride(pkg, "v1.0.0"))
}

func TestResolveVersionOverride_ChecksumDisabledPerOS(t *testing.T) {
	t.Parallel()

	// An override that matches but disables checksum should merge enabled:false
	// onto the base checksum.
	y := `
type: github_release
repo_owner: acme
repo_name: tool
checksum:
  type: github_release
  asset: checksums.txt
  algorithm: sha256
version_constraint: "false"
version_overrides:
  - version_constraint: "true"
    asset: tool-{{.Version}}.tar.gz
    format: tar.gz
    checksum:
      enabled: false
`
	pkg := parsePackage(t, y)
	resolved := resolveVersionOverride(pkg, "v1.0.0")
	require.NotNil(t, resolved.Checksum)
	assert.False(t, resolved.Checksum.isEnabled())
	// Base fields preserved through the merge.
	assert.Equal(t, "checksums.txt", resolved.Checksum.Asset)
}

func TestResolveVersionOverride_NoAsset(t *testing.T) {
	t.Parallel()

	y := `
type: github_release
repo_owner: acme
repo_name: tool
version_constraint: "false"
version_overrides:
  - version_constraint: Version == "v1.0.0"
    no_asset: true
  - version_constraint: "true"
    asset: tool-{{.Version}}.tar.gz
    format: tar.gz
`
	pkg := parsePackage(t, y)

	yanked := resolveVersionOverride(pkg, "v1.0.0")
	assert.True(t, yanked.NoAsset)

	ok := resolveVersionOverride(pkg, "v2.0.0")
	assert.False(t, ok.NoAsset)
	assert.Equal(t, "tool-{{.Version}}.tar.gz", ok.Asset)
}

func TestResolveVersionOverride_TypeChangePerVersion(t *testing.T) {
	t.Parallel()

	// Older versions installed via go_build, newer via github_release.
	y := `
type: github_release
repo_owner: acme
repo_name: tool
version_constraint: "false"
version_overrides:
  - version_constraint: semver("< 1.0.0")
    type: go_build
    path: github.com/acme/tool
  - version_constraint: "true"
    asset: tool-{{.Version}}.tar.gz
    format: tar.gz
`
	pkg := parsePackage(t, y)

	old := resolveVersionOverride(pkg, "v0.5.0")
	assert.True(t, old.IsGoPackage())
	assert.Equal(t, "github.com/acme/tool", old.GoInstallPath)

	current := resolveVersionOverride(pkg, "v1.2.0")
	assert.False(t, current.IsGoPackage())
}

func TestInstallE2E_VersionOverride_SelectsAsset(t *testing.T) {
	t.Setenv("DOCKER_AGENT_TOOLS_DIR", t.TempDir())

	binaryContent := []byte("#!/bin/sh\necho overridden")
	manifest := sha256Hex(binaryContent) + "  tool\n"

	y := `
type: github_release
repo_owner: acme
repo_name: tool
version_constraint: "false"
version_overrides:
  - version_constraint: "true"
    asset: tool
    format: raw
    files:
      - name: tool
    checksum:
      type: github_release
      asset: checksums.txt
      algorithm: sha256
`
	pkg := parsePackage(t, y)

	registry := urlRegistry(map[string][]byte{
		"tool":          binaryContent,
		"checksums.txt": []byte(manifest),
	})

	result, err := registry.Install(t.Context(), pkg, "v1.0.0")
	require.NoError(t, err)
	assertInstalledBinary(t, result, string(binaryContent), "tool")
}

func TestInstallE2E_VersionOverride_NoAssetFails(t *testing.T) {
	t.Setenv("DOCKER_AGENT_TOOLS_DIR", t.TempDir())

	y := `
type: github_release
repo_owner: acme
repo_name: tool
version_constraint: "false"
version_overrides:
  - version_constraint: "true"
    no_asset: true
`
	pkg := parsePackage(t, y)

	_, err := urlRegistry(nil).Install(t.Context(), pkg, "v1.0.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no downloadable asset")
}

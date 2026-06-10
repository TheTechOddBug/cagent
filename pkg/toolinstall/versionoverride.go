package toolinstall

import (
	"log/slog"
	"maps"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/expr-lang/expr"
)

// resolveVersionOverride returns a package view for a specific version with the
// first matching aqua "version_override" applied. Packages without
// version_overrides are returned unchanged.
//
// aqua evaluates the base package's own constraint first (commonly "false" so
// it never matches on its own), then each override top-to-bottom; the first
// match wins and its fields are layered over the base.
func resolveVersionOverride(pkg *Package, version string) *Package {
	if len(pkg.VersionOverrides) == 0 {
		return pkg
	}

	if pkg.VersionConstraint != "" && evalVersionConstraint(pkg.VersionConstraint, version) {
		return pkg
	}

	for i := range pkg.VersionOverrides {
		vo := &pkg.VersionOverrides[i]
		if evalVersionConstraint(vo.VersionConstraint, version) {
			return applyVersionOverride(pkg, vo)
		}
	}

	return pkg
}

// applyVersionOverride layers a matched override's non-empty fields onto a copy
// of the base package. Replacements and checksum are merged; everything else
// replaces the base value when set.
func applyVersionOverride(base *Package, vo *VersionOverride) *Package {
	resolved := *base
	resolved.VersionOverrides = nil

	if vo.Type != "" {
		resolved.Type = vo.Type
	}
	if vo.Asset != "" {
		resolved.Asset = vo.Asset
	}
	if vo.Format != "" {
		resolved.Format = vo.Format
	}
	if len(vo.Files) > 0 {
		resolved.Files = vo.Files
	}
	if len(vo.Overrides) > 0 {
		resolved.Overrides = vo.Overrides
	}
	if vo.GoInstallPath != "" {
		resolved.GoInstallPath = vo.GoInstallPath
	}
	if vo.VersionPrefix != "" {
		resolved.VersionPrefix = vo.VersionPrefix
	}
	if len(vo.SupportedEnvs) > 0 {
		resolved.SupportedEnvs = vo.SupportedEnvs
	}
	if vo.Checksum != nil {
		resolved.Checksum = mergeChecksum(resolved.Checksum, vo.Checksum)
	}
	if len(vo.Replacements) > 0 {
		merged := make(map[string]string, len(resolved.Replacements)+len(vo.Replacements))
		maps.Copy(merged, resolved.Replacements)
		maps.Copy(merged, vo.Replacements)
		resolved.Replacements = merged
	}
	if vo.NoAsset != nil {
		resolved.NoAsset = *vo.NoAsset
	}

	return &resolved
}

// evalVersionConstraint evaluates an aqua version_constraint expression against
// a version. An empty constraint matches. Compilation or evaluation errors are
// treated as "no match" (logged) so a single malformed registry entry cannot
// abort resolution.
//
// The expression language is aqua's: the variable "Version" plus the functions
// semver/semverWithVersion/trimPrefix; string operators like startsWith,
// matches, and in are provided natively by expr-lang.
func evalVersionConstraint(constraint, version string) bool {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return true
	}

	out, err := expr.Eval(constraint, constraintEnv(version))
	if err != nil {
		slog.Warn("Failed to evaluate aqua version_constraint",
			"constraint", constraint, "version", version, "error", err)
		return false
	}

	matched, ok := out.(bool)
	return ok && matched
}

func constraintEnv(version string) map[string]any {
	return map[string]any{
		"Version": version,
		"semver": func(constraint string) bool {
			return semverSatisfies(version, constraint)
		},
		"semverWithVersion": func(constraint, v string) bool {
			return semverSatisfies(v, constraint)
		},
		"trimPrefix": strings.TrimPrefix,
	}
}

// semverSatisfies reports whether version satisfies the semver constraint range.
// Invalid versions or constraints yield false so a malformed entry can never
// accidentally match. Matches aqua's range semantics via Masterminds/semver,
// which (like npm/cargo) excludes pre-releases unless the range names one.
func semverSatisfies(version, constraint string) bool {
	c, err := semver.NewConstraint(constraint)
	if err != nil {
		return false
	}
	v, err := semver.NewVersion(version)
	if err != nil {
		return false
	}
	return c.Check(v)
}

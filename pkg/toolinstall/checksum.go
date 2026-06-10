package toolinstall

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// maxChecksumFileSize bounds the checksum file download. Real checksum
// manifests are a few KiB at most; the limit guards against an
// attacker-controlled release streaming an unbounded body.
const maxChecksumFileSize = 1 << 20 // 1 MiB

// Checksum mirrors the aqua registry "checksum" schema describing how to
// verify the integrity of a downloaded release asset.
type Checksum struct {
	Type      string `yaml:"type"`
	Asset     string `yaml:"asset"`
	URL       string `yaml:"url"`
	Algorithm string `yaml:"algorithm"`
	Enabled   *bool  `yaml:"enabled"`
}

// isEnabled reports whether checksum verification should be attempted. A
// checksum block defaults to enabled when present unless explicitly disabled
// (e.g. an aqua platform override setting "enabled: false").
func (c *Checksum) isEnabled() bool {
	if c == nil {
		return false
	}
	return c.Enabled == nil || *c.Enabled
}

// mergeChecksum overlays the non-empty fields of override onto base. It mirrors
// how aqua platform overrides amend a package's checksum config — most commonly
// to disable verification on a specific OS via "enabled: false".
func mergeChecksum(base, override *Checksum) *Checksum {
	if base == nil {
		return override
	}
	if override == nil {
		return base
	}

	merged := *base
	if override.Type != "" {
		merged.Type = override.Type
	}
	if override.Asset != "" {
		merged.Asset = override.Asset
	}
	if override.URL != "" {
		merged.URL = override.URL
	}
	if override.Algorithm != "" {
		merged.Algorithm = override.Algorithm
	}
	if override.Enabled != nil {
		merged.Enabled = override.Enabled
	}
	return &merged
}

// newHasher returns a hash for the given algorithm. Only strong algorithms are
// supported; weaker ones (md5/sha1) provide little integrity guarantee and are
// reported as unsupported so verification is skipped rather than giving a false
// sense of security.
func newHasher(algorithm string) (hash.Hash, error) {
	switch strings.ToLower(algorithm) {
	case "", "sha256":
		return sha256.New(), nil
	case "sha512":
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("unsupported checksum algorithm %q", algorithm)
	}
}

// verifyAssetChecksum downloads the checksum manifest advertised by the package,
// extracts the expected digest for assetName, and compares it against the digest
// of the asset stored at assetPath.
//
// It fails closed: once a package advertises an (enabled, supported) checksum,
// any failure to download, parse, or match the digest aborts the installation.
// Unsupported checksum types or algorithms are skipped with a warning rather
// than failing, to avoid breaking installs the registry can't help us verify.
//
// Threat model: the manifest is fetched from the same GitHub release as the
// asset, so this primarily guards against in-transit corruption and a tampered
// download CDN. It does NOT defend against a fully compromised release or repo
// (where an attacker controls both files) — that requires pinned digests or
// cosign/SLSA provenance, which the registry also exposes and is future work.
func (r *Registry) verifyAssetChecksum(ctx context.Context, pkg *Package, version, assetName, assetPath string, c *Checksum, data templateData) error {
	pkgName := pkg.RepoOwner + "/" + pkg.RepoName

	if c.Type != "" && c.Type != "github_release" {
		slog.WarnContext(ctx, "Skipping checksum verification: unsupported checksum type",
			"type", c.Type, "package", pkgName)
		return nil
	}

	hasher, err := newHasher(c.Algorithm)
	if err != nil {
		slog.WarnContext(ctx, "Skipping checksum verification", "reason", err, "package", pkgName)
		return nil
	}

	checksumAsset, err := renderTemplate(c.Asset, data)
	if err != nil {
		return fmt.Errorf("rendering checksum asset template: %w", err)
	}
	if checksumAsset == "" {
		slog.WarnContext(ctx, "Skipping checksum verification: no checksum asset", "package", pkgName)
		return nil
	}

	url := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s",
		pkg.RepoOwner, pkg.RepoName, version, checksumAsset)

	body, err := r.download(ctx, url)
	if err != nil {
		return fmt.Errorf("downloading checksum file %s: %w", checksumAsset, err)
	}
	defer body.Close()

	manifest, err := io.ReadAll(io.LimitReader(body, maxChecksumFileSize))
	if err != nil {
		return fmt.Errorf("reading checksum file %s: %w", checksumAsset, err)
	}

	expected, err := parseChecksumFile(manifest, assetName)
	if err != nil {
		return fmt.Errorf("checksum file %s: %w", checksumAsset, err)
	}

	f, err := os.Open(assetPath)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(hasher, f); err != nil {
		return fmt.Errorf("hashing asset %s: %w", assetName, err)
	}
	actual := hex.EncodeToString(hasher.Sum(nil))

	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", assetName, expected, actual)
	}

	slog.InfoContext(ctx, "Verified asset checksum",
		"package", pkgName, "asset", assetName, "algorithm", c.Algorithm)
	return nil
}

// parseChecksumFile extracts the hex digest for assetName from a checksum
// manifest. It supports both multi-entry "<digest>  <file>" manifests (e.g.
// checksums.txt) and single-line files containing only a digest. The "*"
// binary-mode marker some tools prepend to filenames is tolerated.
func parseChecksumFile(data []byte, assetName string) (string, error) {
	var lines []string
	for l := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}

	if len(lines) == 1 {
		if fields := strings.Fields(lines[0]); len(fields) == 1 {
			return fields[0], nil
		}
	}

	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name == assetName || filepath.Base(name) == assetName {
			return fields[0], nil
		}
	}

	return "", fmt.Errorf("no checksum entry found for asset %q", assetName)
}

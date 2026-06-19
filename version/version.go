// Package version provides version parsing helpers shared by clive
// and its subpackages. It wraps hashicorp/go-version with prefix stripping,
// dev-version detection, and natural-sort prerelease comparison.
package version

import (
	"strconv"
	"strings"

	goversion "github.com/hashicorp/go-version"
)

const (
	prefix = "v"

	markerGit = "-g" // git describe --long hash prefix, e.g. "-g55ae225"
	markerDev = "-dev"
)

// HasPrefix reports whether v starts with a "v"/"V" version prefix.
func HasPrefix(v string) bool {
	return len(v) > 0 && (v[0] == 'v' || v[0] == 'V')
}

// AddPrefix prepends "v" if v is non-empty and not already prefixed.
func AddPrefix(v string) string {
	if v == "" || HasPrefix(v) {
		return v
	}
	return prefix + v
}

// RemovePrefix strips a leading "v"/"V" if present.
func RemovePrefix(v string) string {
	if HasPrefix(v) {
		return v[1:]
	}
	return v
}

// Parse normalizes and parses a version string into a [goversion.Version].
// It strips a "v" prefix and drops anything after a "-g<hash>" git suffix;
// go-version pads versions with fewer than three components ("1.2" -> "1.2.0").
func Parse(v string) (*goversion.Version, error) {
	v = RemovePrefix(v)

	if idx := strings.Index(v, markerGit); idx > 0 {
		v = v[:idx]
	}

	return goversion.NewVersion(v)
}

// IsDev reports whether v looks like a development build.
//
// Two formats are recognised:
//   - git describe: vX.Y.Z-N-gHASH (N commits ahead of tag X.Y.Z)
//   - dev suffix:   vX.Y.Z-HASH-dev or vX.Y.Z-N-gHASH-dev
//
// Both indicate commits AHEAD of a tagged release, not prereleases.
func IsDev(v string) bool {
	v = RemovePrefix(v)

	if strings.HasSuffix(v, markerDev) {
		return true
	}

	idx := strings.LastIndex(v, markerGit)
	if idx <= 0 {
		return false
	}

	prefix := v[:idx]
	lastDash := strings.LastIndex(prefix, "-")
	if lastDash < 0 {
		return false
	}

	commitCount := prefix[lastDash+1:]
	return isAllDigits(commitCount)
}

// CommitCount returns the number of commits since the last tag embedded in a
// git-describe-formatted version string (e.g. "v1.2.3-4-gabcdef[-dev]"), or 0.
func CommitCount(v string) int {
	v = strings.TrimSuffix(RemovePrefix(v), markerDev)
	idx := strings.LastIndex(v, markerGit)
	if idx <= 0 {
		return 0
	}
	prefix := v[:idx]
	lastDash := strings.LastIndex(prefix, "-")
	if lastDash < 0 {
		return 0
	}
	countStr := prefix[lastDash+1:]
	if !isAllDigits(countStr) {
		return 0
	}
	n, _ := strconv.Atoi(countStr)
	return n
}

// ExtractBase recovers the underlying release version from a dev version.
//
// Examples:
//   - "0.20.8-2-g55ae225"      -> "0.20.8"
//   - "0.21.3-2-g55ae225-dev"  -> "0.21.3"
//   - "0.21.3-3b71351-dev"     -> "0.21.3"
//
// For non-dev inputs the v-prefix is stripped and the rest returned as-is.
func ExtractBase(v string) string {
	v = RemovePrefix(v)

	if trimmed, found := strings.CutSuffix(v, markerDev); found {
		return extractDevBase(trimmed)
	}
	return stripGitDescribe(v)
}

// extractDevBase recovers the base version from a -dev-suffixed string.
// Handles both the new "base-N-gHASH" form and the old "base-HASH" form.
func extractDevBase(trimmed string) string {
	if base, ok := stripCountedGitHash(trimmed); ok {
		return base
	}
	// Old format: "0.21.3-3b71351" - strip the trailing -HASH
	if lastDash := strings.LastIndex(trimmed, "-"); lastDash > 0 {
		return trimmed[:lastDash]
	}
	return trimmed
}

// stripCountedGitHash removes a "-N-gHASH" suffix and returns the base.
// Reports false if the input doesn't end in that shape.
func stripCountedGitHash(v string) (string, bool) {
	idx := strings.LastIndex(v, markerGit)
	if idx <= 0 {
		return "", false
	}
	prefix := v[:idx]
	lastDash := strings.LastIndex(prefix, "-")
	if lastDash <= 0 {
		return "", false
	}
	if !isAllDigits(prefix[lastDash+1:]) {
		return "", false
	}
	return prefix[:lastDash], true
}

// stripGitDescribe removes a "-N-gHASH" suffix from a non-dev-suffixed
// version, returning the input unchanged when no such suffix is present.
func stripGitDescribe(v string) string {
	if base, ok := stripCountedGitHash(v); ok {
		return base
	}
	return v
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

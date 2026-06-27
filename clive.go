// Package clive provides version detection, display, and "latest version"
// lookup for Go CLIs.
//
// Callers inject build metadata via -ldflags:
//
//	-X github.com/gechr/clive.version=$(VERSION)
//	-X github.com/gechr/clive.buildTime=$(BUILDTIME)
//
// When ldflags are not set (e.g. `go install ...@latest` or `go build`
// without flags), Current falls back to debug.BuildInfo so the binary
// still reports a sensible version string.
package clive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	ver "github.com/gechr/clive/version"
	xansi "github.com/gechr/x/ansi"
	"github.com/gechr/x/human"
	xstrings "github.com/gechr/x/strings"
	"github.com/gechr/x/terminal"
	goversion "github.com/hashicorp/go-version"
)

// Linker-injected build metadata. Set via -ldflags; left empty otherwise.
var (
	version   string
	commit    string
	buildTime string

	currentOnce sync.Once
	current     string
)

// Info identifies the binary for version-link and latest-lookup purposes.
// The zero value is usable for Current/Print but Latest and VersionLink
// require Module (and Repo for hyperlinks).
type Info struct {
	// Module is the Go module path, e.g. "github.com/gechr/clone".
	// Required by Latest.
	Module string

	// Repo is the GitHub "owner/name" used to build release/commit URLs.
	// If empty and Module starts with "github.com/", Repo is derived from it.
	Repo string

	// Private resolves Latest via direct version control rather than the public
	// module proxy, by scoping GOPRIVATE to Module for that one `go list` call.
	// Set it for a tool published from a private repository, so the lookup uses
	// the caller's local git credentials (e.g. SSH keys) instead of failing
	// against a proxy that cannot see the module. It has no effect on a public
	// module and never mutates the process environment.
	Private bool
}

// repo returns Repo, deriving it from Module when unset.
func (i Info) repo() string {
	if i.Repo != "" {
		return i.Repo
	}
	if rest, ok := strings.CutPrefix(i.Module, "github.com/"); ok {
		return rest
	}
	return ""
}

// Current returns the version string for the running binary, computing it
// once and caching the result.
//
// Resolution order:
//  1. ldflag-injected `version` (Makefile path)
//  2. debug.BuildInfo Main.Version (Go module proxy / `go install`)
//  3. debug.BuildInfo vcs.revision (plain `go build`)
//
// Dev versions are normalised so a trailing "-dev" is preserved or appended
// as appropriate.
func Current() string {
	currentOnce.Do(func() {
		v := ver.RemovePrefix(version)
		switch {
		case v == "":
			v = tryRevision()
		case commit == "":
			// version came from `git describe` (0.21.4-1-g4bed8a3) or was
			// already reformatted by the Makefile (0.21.4-1-g4bed8a3-dev).
			// Either way, ensure a -dev suffix and preserve the commit count.
			if ver.IsDev(v) && !strings.HasSuffix(v, "-dev") {
				v = format(v + "-dev")
			} else {
				v = format(v)
			}
		default:
			v = format(v)
		}
		current = v
	})
	return current
}

// Print writes Current to stdout, or a friendly placeholder when unknown.
func Print() {
	if v := Current(); v != "" {
		fmt.Println(v)
	} else {
		fmt.Println("Version information is not available")
	}
}

// PrintDetailed writes a labelled table of version, Go runtime, OS/arch,
// build time, and VCS info. When i.Repo (or i.Module) is set, the version
// row is rendered as a clickable terminal hyperlink.
func (i Info) PrintDetailed() {
	v := Current()
	if v == "" {
		v = "(unknown)"
	}

	rows := [][2]string{{"Version", i.VersionLink(v)}}
	if n := ver.CommitCount(v); n > 0 {
		rows = append(rows, [2]string{"Commits since tag", fmt.Sprintf("%d", n)})
	}
	rows = append(rows,
		[2]string{"Go version", runtime.Version()},
		[2]string{"OS/Arch", runtime.GOOS + "/" + runtime.GOARCH},
	)

	if buildTime != "" {
		val := buildTime
		if t, err := time.Parse(time.RFC3339, buildTime); err == nil {
			val = fmt.Sprintf("%s (%s)", buildTime, human.FormatTimeAgo(t))
		}
		rows = append(rows, [2]string{"Built", val})
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		var rev, modified string
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				modified = s.Value
			}
		}
		if rev != "" {
			rows = append(rows, [2]string{"Commit", rev})
		}
		if modified == "true" {
			rows = append(rows, [2]string{"Dirty", "true"})
		}
	}

	maxWidth := 0
	for _, r := range rows {
		if len(r[0]) > maxWidth {
			maxWidth = len(r[0])
		}
	}
	for _, r := range rows {
		fmt.Printf("%-*s  %s\n", maxWidth+1, r[0]+":", r[1])
	}
}

// VersionURL returns the GitHub URL for v: the release-tag page for a release
// version, or the commit page for a dev version. It returns "" when i has no
// usable Repo or v has no derivable URL, so a caller can fall back to plain text.
func (i Info) VersionURL(v string) string {
	if v == "" {
		return ""
	}
	repo := i.repo()
	if repo == "" {
		return ""
	}

	if hash := extractCommitHash(v); hash != "" {
		linkURL, _ := url.JoinPath("https://github.com", repo, "commit", hash)
		return linkURL
	}

	if _, err := goversion.NewVersion(v); err == nil {
		linkURL, _ := url.JoinPath(
			"https://github.com", repo, "releases", "tag", ver.AddPrefix(v),
		)
		return linkURL
	}
	return ""
}

// VersionLink returns v rendered as a clickable terminal hyperlink to the
// matching GitHub tag (release versions) or commit (dev versions).
// If i has no usable Repo, v is returned unchanged.
func (i Info) VersionLink(v string) string {
	link := i.VersionURL(v)
	if link == "" {
		return v
	}
	return hyperlink(link, v)
}

// hyperlink renders an OSC 8 terminal hyperlink when stdout is a terminal,
// degrading to just the display text (no URL) otherwise.
func hyperlink(url, text string) string {
	a := xansi.New(
		xansi.WithTerminal(terminal.Is(os.Stdout)),
		xansi.WithHyperlinkFallback(xansi.HyperlinkFallbackText),
	)
	return a.Hyperlink(url, text)
}

// UpdateAvailable reports whether a newer version of i.Module is available on
// the module proxy than the currently running binary. It returns (false, nil)
// when the current version cannot be parsed or is already the latest.
// Requires `go` on PATH.
func (i Info) UpdateAvailable(ctx context.Context) (bool, error) {
	latestRaw, err := i.Latest(ctx)
	if err != nil {
		return false, err
	}
	return isNewer(Current(), latestRaw), nil
}

// isNewer reports whether latestRaw is a strictly greater semver than currentRaw.
// Returns false if either string cannot be parsed as a semver.
func isNewer(currentRaw, latestRaw string) bool {
	cur, err := goversion.NewVersion(currentRaw)
	if err != nil {
		return false
	}
	lat, err := goversion.NewVersion(latestRaw)
	if err != nil {
		return false
	}
	return ver.GreaterThan(lat, cur)
}

// Latest queries the Go module proxy for the latest published version of
// i.Module. Returns the raw `result.Version` string from `go list -m -json`.
// Requires `go` on PATH.
func (i Info) Latest(ctx context.Context) (string, error) {
	if i.Module == "" {
		return "", fmt.Errorf("clive: Latest requires Info.Module")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		return "", fmt.Errorf("go toolchain not found on PATH: %w", err)
	}

	cmd := exec.CommandContext(ctx, goBin, "list", "-m", "-json", i.Module+"@latest")
	if i.Private {
		cmd.Env = append(os.Environ(), "GOPRIVATE="+goPrivate(i.Module, os.Getenv("GOPRIVATE")))
	}
	stdout, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go list -m: %w", err)
	}

	var result struct {
		Version string `json:"Version"` //nolint:tagliatelle // matches `go list -m -json` output
	}
	if err := json.Unmarshal(stdout, &result); err != nil {
		return "", fmt.Errorf("parse go list output: %w", err)
	}
	return result.Version, nil
}

// goPrivate returns a GOPRIVATE value that includes module, preserving any
// existing entries so the caller's configuration is not discarded. Both inputs
// are trimmed so surrounding whitespace never yields a malformed list.
func goPrivate(module, existing string) string {
	module = strings.TrimSpace(module)
	existing = strings.TrimSpace(existing)
	if existing == "" {
		return module
	}
	return module + "," + existing
}

// format normalises a non-empty version into the canonical "vX.Y.Z[...]"
// shape: ensures a leading "v" and trims a trailing "-".
func format(v string) string {
	return "v" + strings.TrimSuffix(ver.RemovePrefix(v), "-")
}

// pseudoVersionParts is the number of dash-separated parts in a Go
// pseudo-version: base-timestamp-hash.
const pseudoVersionParts = 3

// tryRevision falls back to debug.BuildInfo when no ldflag version is set, so a
// `go install module@version` (or plain `go build`) binary still reports a
// sensible version.
func tryRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return DeriveVersion(info.Main.Version, vcsRevision(info))
}

// DeriveVersion resolves a display version from Go build metadata. A module
// version (set by `go install module@version` via the module proxy) is
// preferred over a VCS revision (from a plain `go build`). It returns "" when
// neither is available. [Current] uses it as the fallback when no version was
// injected via ldflags.
func DeriveVersion(moduleVersion, revision string) string {
	if moduleVersion != "" && moduleVersion != "(devel)" {
		mv := ver.RemovePrefix(moduleVersion)
		parts := strings.Split(mv, "-")
		if len(parts) != pseudoVersionParts {
			return format(mv) // a tagged release, e.g. v1.2.3
		}
		// A pseudo-version (base-timestamp-hash): keep the base and short hash.
		return format(fmt.Sprintf("%s-g%.7s-dev", parts[0], parts[2]))
	}
	if revision != "" {
		return format(fmt.Sprintf("0.0.0-g%.7s-dev", ver.RemovePrefix(revision)))
	}
	return ""
}

// vcsRevision returns the build's VCS revision, or "" when not embedded.
func vcsRevision(info *debug.BuildInfo) string {
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return ""
}

// extractCommitHash pulls a commit hash out of a dev-format version string,
// or "" if v is a plain release.
func extractCommitHash(v string) string {
	v = ver.RemovePrefix(v)
	// "X.Y.Z-N-gHASH" or "X.Y.Z-N-gHASH-dev"
	if idx := strings.LastIndex(v, "-g"); idx > 0 {
		rest := v[idx+2:]
		rest = strings.TrimSuffix(rest, "-dev")
		if isHex(rest) {
			return rest
		}
	}
	// Old "X.Y.Z-HASH-dev" with no -g marker
	if rest, ok := strings.CutSuffix(v, "-dev"); ok {
		if i := strings.LastIndex(rest, "-"); i > 0 {
			cand := rest[i+1:]
			if isHex(cand) {
				return cand
			}
		}
	}
	return ""
}

func isHex(s string) bool {
	if len(s) < 7 { //nolint:mnd // git --abbrev=7.
		return false
	}
	return xstrings.IsHex(s)
}

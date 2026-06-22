package clive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	ver "github.com/gechr/clive/version"
	goversion "github.com/hashicorp/go-version"
)

const (
	// githubAPI is the GitHub REST base used for tag lookups.
	githubAPI = "https://api.github.com"

	// tagPageSize caps a tags request to the newest page. One page is enough to
	// find the latest release and keeps an unauthenticated check well within the
	// rate limit.
	tagPageSize = 100
)

// tagRef is the slice of a GitHub tags-list entry that [Info.LatestTag] reads.
type tagRef struct {
	Name string `json:"name"`
}

// LatestTag returns the highest semver-shaped tag published in i's GitHub
// repository, read from the repository's tags list. Unlike
// [Info.Latest], which shells out to the Go toolchain, it needs only network
// access, so a distributed binary - which has no `go` on PATH - can call it. A
// nil client uses [http.DefaultClient]. It returns "" with a nil error when the
// repository publishes no semver-shaped tag.
func (i Info) LatestTag(ctx context.Context, client *http.Client) (string, error) {
	repo := i.repo()
	if repo == "" {
		return "", fmt.Errorf(
			"clive: LatestTag needs a GitHub repo; set Info.Repo or a github.com module",
		)
	}
	if client == nil {
		client = http.DefaultClient
	}

	endpoint, err := url.JoinPath(githubAPI, "repos", repo, "tags")
	if err != nil {
		return "", fmt.Errorf("clive: build tags URL: %w", err)
	}
	endpoint += fmt.Sprintf("?per_page=%d", tagPageSize)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("clive: build tags request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("clive: fetch tags: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("clive: fetch tags: unexpected status %s", resp.Status)
	}

	var tags []tagRef
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return "", fmt.Errorf("clive: decode tags: %w", err)
	}

	return highestTag(tags), nil
}

// highestTag returns the name of the greatest semver-shaped tag, or "" when none
// of the names parse as a version. The raw tag name is returned so a leading
// "v" prefix is preserved for display.
func highestTag(tags []tagRef) string {
	var best *goversion.Version
	var name string
	for _, t := range tags {
		v, err := ver.Parse(t.Name)
		if err != nil {
			continue
		}
		if best == nil || ver.GreaterThan(v, best) {
			best, name = v, t.Name
		}
	}
	return name
}

package clive_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gechr/clive"
	"github.com/stretchr/testify/require"
)

// roundTripFunc adapts a function to an http.RoundTripper, the seam that serves
// canned GitHub responses without touching the network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestLatestTagPicksHighestSemver(t *testing.T) {
	t.Parallel()

	body := `[{"name":"v1.2.0"},{"name":"v1.10.0"},{"name":"v1.9.0"},{"name":"nightly"}]`
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			require.Equal(t, "api.github.com", req.URL.Host)
			require.Equal(t, "/repos/gechr/clover/tags", req.URL.Path)
			require.Equal(t, "100", req.URL.Query().Get("per_page"))
			return jsonResponse(http.StatusOK, body), nil
		}),
	}

	got, err := clive.Info{
		Module: "github.com/gechr/clover",
	}.LatestTag(
		context.Background(),
		client,
	)
	require.NoError(t, err)
	require.Equal(t, "v1.10.0", got, "natural order ranks 1.10 above 1.9")
}

func TestLatestTagNoSemverTags(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `[{"name":"latest"},{"name":"nightly"}]`), nil
	})}

	got, err := clive.Info{Repo: "gechr/clover"}.LatestTag(context.Background(), client)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestLatestTagBadStatus(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusForbidden, ""), nil
	})}

	_, err := clive.Info{Module: "github.com/gechr/clover"}.LatestTag(context.Background(), client)
	require.Error(t, err)
}

func TestLatestTagNoRepo(t *testing.T) {
	t.Parallel()

	_, err := clive.Info{}.LatestTag(context.Background(), nil)
	require.Error(t, err)
}

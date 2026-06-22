package notify

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gechr/clive"
	"github.com/stretchr/testify/require"
)

// roundTripFunc adapts a function to an http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// tagsTransport serves a single tag, v1.2.0, recording each request through
// count (when non-nil) so a test can assert a skipped check never hit the
// network.
func tagsTransport(count *int) http.RoundTripper {
	return roundTripFunc(func(*http.Request) (*http.Response, error) {
		if count != nil {
			*count++
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`[{"name":"v1.2.0"}]`)),
			Header:     make(http.Header),
		}, nil
	})
}

func info() clive.Info { return clive.Info{Module: "github.com/me/myapp"} }

func TestNewer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{"patch behind", "v1.0.0", "v1.0.1", true},
		{"equal", "v1.2.0", "v1.2.0", false},
		{"ahead", "v2.0.0", "v1.9.9", false},
		{"natural order", "v1.9.0", "v1.10.0", true},
		{"dev build past latest tag", "v0.21.4-1-g4bed8a3-dev", "v0.21.4", false},
		{"dev build behind a real release", "v0.21.4-1-g4bed8a3-dev", "v0.22.0", true},
		{"empty current", "", "v1.0.0", false},
		{"unparseable latest", "v1.0.0", "garbage", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, newer(tc.current, tc.latest))
		})
	}
}

func TestEnvVar(t *testing.T) {
	t.Parallel()
	require.Equal(t, "MYAPP_NO_UPDATE_CHECK", (&checker{name: "myapp"}).envVar())
}

func TestEvaluateReportsNewer(t *testing.T) {
	t.Setenv("MYAPP_NO_UPDATE_CHECK", "")

	c := newChecker(info(), "myapp",
		WithCacheDir(t.TempDir()),
		WithCurrentVersion("v1.0.0"),
		WithTransport(tagsTransport(nil)),
	)

	latest, ok := c.evaluate(context.Background())
	require.True(t, ok)
	require.Equal(t, "v1.2.0", latest)
}

func TestEvaluateSilentWhenUpToDate(t *testing.T) {
	t.Setenv("MYAPP_NO_UPDATE_CHECK", "")

	c := newChecker(info(), "myapp",
		WithCacheDir(t.TempDir()),
		WithCurrentVersion("v1.2.0"),
		WithTransport(tagsTransport(nil)),
	)

	_, ok := c.evaluate(context.Background())
	require.False(t, ok)
}

func TestEvaluateDisabledByEnv(t *testing.T) {
	t.Setenv("MYAPP_NO_UPDATE_CHECK", "1")

	calls := 0
	c := newChecker(info(), "myapp",
		WithCacheDir(t.TempDir()),
		WithCurrentVersion("v1.0.0"),
		WithTransport(tagsTransport(&calls)),
	)

	_, ok := c.evaluate(context.Background())
	require.False(t, ok)
	require.Zero(t, calls, "a disabled check must not hit the network")
}

func TestEvaluateRespectsCooldown(t *testing.T) {
	t.Setenv("MYAPP_NO_UPDATE_CHECK", "")

	dir := t.TempDir()
	// A fresh stamp means a check happened within the cooldown window.
	require.NoError(t, os.WriteFile(filepath.Join(dir, stampName), nil, stampPerm))

	calls := 0
	c := newChecker(info(), "myapp",
		WithCacheDir(dir),
		WithCurrentVersion("v1.0.0"),
		WithTransport(tagsTransport(&calls)),
	)

	_, ok := c.evaluate(context.Background())
	require.False(t, ok)
	require.Zero(t, calls, "a check within the cooldown must not hit the network")
}

func TestEvaluateChecksAfterCooldown(t *testing.T) {
	t.Setenv("MYAPP_NO_UPDATE_CHECK", "")

	dir := t.TempDir()
	stamp := filepath.Join(dir, stampName)
	require.NoError(t, os.WriteFile(stamp, nil, stampPerm))
	stale := time.Now().Add(-cooldown - time.Hour)
	require.NoError(t, os.Chtimes(stamp, stale, stale))

	c := newChecker(info(), "myapp",
		WithCacheDir(dir),
		WithCurrentVersion("v1.0.0"),
		WithTransport(tagsTransport(nil)),
	)

	latest, ok := c.evaluate(context.Background())
	require.True(t, ok)
	require.Equal(t, "v1.2.0", latest)
}

func TestEvaluateStampsBeforeFetch(t *testing.T) {
	t.Setenv("MYAPP_NO_UPDATE_CHECK", "")

	dir := t.TempDir()
	c := newChecker(info(), "myapp",
		WithCacheDir(dir),
		WithCurrentVersion("v1.0.0"),
		WithTransport(tagsTransport(nil)),
	)

	_, _ = c.evaluate(context.Background())

	_, err := os.Stat(filepath.Join(dir, stampName))
	require.NoError(t, err, "evaluate must write the cooldown stamp")
}

package notify

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gechr/clive"
	"github.com/stretchr/testify/require"
)

// roundTripFunc adapts a function to an http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// tagsTransport serves a single tag, v1.2.0.
func tagsTransport() http.RoundTripper {
	return roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, `[{"name":"v1.2.0"}]`), nil
	})
}

// errTransport always answers with a server error.
func errTransport() http.RoundTripper {
	return roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusInternalServerError, ""), nil
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

func TestReadWriteStamp(t *testing.T) {
	t.Parallel()

	c := newChecker(info(), "myapp", WithCacheDir(t.TempDir()))
	c.writeStamp("v3.1.4")

	latest, when, cached := c.readStamp()
	require.True(t, cached)
	require.Equal(t, "v3.1.4", latest)
	require.WithinDuration(t, time.Now(), when, time.Minute)
}

func TestReadStampMissing(t *testing.T) {
	t.Parallel()

	c := newChecker(info(), "myapp", WithCacheDir(t.TempDir()))
	_, _, cached := c.readStamp()
	require.False(t, cached)
}

func TestRefreshWritesLatest(t *testing.T) {
	t.Parallel()

	c := newChecker(info(), "myapp", WithCacheDir(t.TempDir()), WithTransport(tagsTransport()))
	c.refresh()

	latest, _, cached := c.readStamp()
	require.True(t, cached)
	require.Equal(t, "v1.2.0", latest)
}

func TestRefreshThrottlesOnError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	newChecker(info(), "myapp", WithCacheDir(dir)).writeStamp("v1.0.0")

	c := newChecker(info(), "myapp", WithCacheDir(dir), WithTransport(errTransport()))
	c.refresh()

	latest, when, cached := c.readStamp()
	require.True(t, cached)
	require.Equal(t, "v1.0.0", latest, "a failed refresh preserves the prior value")
	require.WithinDuration(t, time.Now(), when, time.Minute, "and re-stamps to honour the cooldown")
}

func TestShouldHintWhenBehind(t *testing.T) {
	t.Parallel()

	c := newChecker(info(), "myapp", WithCacheDir(t.TempDir()), WithCurrentVersion("v1.0.0"))
	c.writeStamp("v1.2.0")

	latest, ok := c.shouldHint()
	require.True(t, ok)
	require.Equal(t, "v1.2.0", latest)
}

func TestShouldHintWhenUpToDate(t *testing.T) {
	t.Parallel()

	c := newChecker(info(), "myapp", WithCacheDir(t.TempDir()), WithCurrentVersion("v1.2.0"))
	c.writeStamp("v1.2.0")

	_, ok := c.shouldHint()
	require.False(t, ok)
}

func TestShouldHintRereadsCache(t *testing.T) {
	t.Parallel()

	// A refresh that lands mid-command updates the cache; because the flush
	// re-reads it, the newer result is shown that same run.
	c := newChecker(info(), "myapp", WithCacheDir(t.TempDir()), WithCurrentVersion("v1.0.0"))
	c.writeStamp("v1.0.0")
	_, ok := c.shouldHint()
	require.False(t, ok, "nothing newer in the cache yet")

	c.writeStamp("v2.0.0") // the background refresh completes
	latest, ok := c.shouldHint()
	require.True(t, ok)
	require.Equal(t, "v2.0.0", latest)
}

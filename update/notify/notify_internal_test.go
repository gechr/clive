package notify

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gechr/clive"
	"github.com/gechr/clive/update/brew"
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

func cfg() brew.Config {
	return brew.Config{
		Info:    clive.Info{Module: "github.com/example/myapp"},
		Formula: "myapp",
	}
}

func differentRef(current, latest string) bool {
	return current != latest
}

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
	require.Equal(
		t,
		"MYAPP_NO_UPDATE_CHECK",
		(&checker{cfg: brew.Config{Formula: "myapp"}}).envVar(),
	)
}

func TestReadWriteStamp(t *testing.T) {
	t.Parallel()

	c := newChecker(cfg(), WithCacheDir(t.TempDir()))
	c.writeStamp("v3.1.4")

	st, when, cached := c.readStamp()
	require.True(t, cached)
	require.Equal(t, "v3.1.4", st.Latest)
	require.Empty(t, st.Track)
	require.Empty(t, st.Skipped)
	require.WithinDuration(t, time.Now(), when, time.Minute)
}

func TestReadStampMissing(t *testing.T) {
	t.Parallel()

	c := newChecker(cfg(), WithCacheDir(t.TempDir()))
	_, _, cached := c.readStamp()
	require.False(t, cached)
}

func TestRefreshWritesLatest(t *testing.T) {
	t.Parallel()

	c := newChecker(cfg(), WithCacheDir(t.TempDir()), WithTransport(tagsTransport()))
	c.refresh()

	st, _, cached := c.readStamp()
	require.True(t, cached)
	require.Equal(t, "v1.2.0", st.Latest)
}

func TestRefreshUsesLatestFunc(t *testing.T) {
	t.Parallel()

	c := newChecker(cfg(), WithCacheDir(t.TempDir()), WithLatestFunc(
		func(context.Context) (string, error) { return "v9.9.9", nil },
	))
	c.refresh()

	st, _, cached := c.readStamp()
	require.True(t, cached)
	require.Equal(t, "v9.9.9", st.Latest, "the override replaces the GitHub-tags lookup")
}

func TestRefreshThrottlesOnError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	newChecker(cfg(), WithCacheDir(dir)).writeStamp("v1.0.0")

	c := newChecker(cfg(), WithCacheDir(dir), WithTransport(errTransport()))
	c.refresh()

	st, when, cached := c.readStamp()
	require.True(t, cached)
	require.Equal(t, "v1.0.0", st.Latest, "a failed refresh preserves the prior value")
	require.WithinDuration(t, time.Now(), when, time.Minute, "and re-stamps to honour the cooldown")
	require.True(t, st.Failed)
}

func TestRefreshErrorSuppressesPriorUpdate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	c := newChecker(cfg(), WithCacheDir(dir), WithCurrentVersion("v1.0.0"))
	c.writeStamp("v2.0.0")

	newChecker(cfg(), WithCacheDir(dir), WithTransport(errTransport())).refresh()

	res, pending := Pending(cfg(), WithCacheDir(dir), WithCurrentVersion("v1.0.0"))
	require.False(t, pending)
	require.Equal(t, "v2.0.0", res.LatestRef)
	require.True(t, res.LatestIsUpdate)
}

func TestShouldHintWhenBehind(t *testing.T) {
	t.Parallel()

	c := newChecker(cfg(), WithCacheDir(t.TempDir()), WithCurrentVersion("v1.0.0"))
	c.writeStamp("v1.2.0")

	latest, ok := c.shouldHint()
	require.True(t, ok)
	require.Equal(t, "v1.2.0", latest)
}

func TestShouldHintWhenUpToDate(t *testing.T) {
	t.Parallel()

	c := newChecker(cfg(), WithCacheDir(t.TempDir()), WithCurrentVersion("v1.2.0"))
	c.writeStamp("v1.2.0")

	_, ok := c.shouldHint()
	require.False(t, ok)
}

func TestShouldHintRereadsCache(t *testing.T) {
	t.Parallel()

	// A refresh that lands mid-command updates the cache; because the flush
	// re-reads it, the newer result is shown that same run.
	c := newChecker(cfg(), WithCacheDir(t.TempDir()), WithCurrentVersion("v1.0.0"))
	c.writeStamp("v1.0.0")
	_, ok := c.shouldHint()
	require.False(t, ok, "nothing newer in the cache yet")

	c.writeStamp("v2.0.0") // the background refresh completes
	latest, ok := c.shouldHint()
	require.True(t, ok)
	require.Equal(t, "v2.0.0", latest)
}

func TestPendingSharesHintVerdictAndFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	display := func(ref string) string { return strings.TrimPrefix(ref, "v") }
	opts := []Option{
		WithCacheDir(dir),
		WithCurrentVersion("v1.0.0"),
		WithChannel("stable"),
		WithRefDisplay(display),
	}
	c := newChecker(cfg(), opts...)
	c.writeStamp("v1.2.0")

	res, pending := Pending(cfg(), opts...)
	latest, hint := c.shouldHint()

	require.True(t, pending)
	require.True(t, hint)
	require.Equal(t, latest, res.LatestRef)
	require.Equal(t, Result{
		CurrentRef:     "v1.0.0",
		LatestRef:      "v1.2.0",
		LatestIsUpdate: true,
		Track:          "stable",
		CurrentDisplay: "1.0.0",
		LatestDisplay:  "1.2.0",
	}, res)
}

func TestPendingWithNoTerminalAndKillSwitch(t *testing.T) {
	dir := t.TempDir()
	c := newChecker(cfg(), WithCacheDir(dir), WithCurrentVersion("v1.0.0"))
	c.writeStamp("v1.2.0")

	stderr := os.Stderr
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	require.NoError(t, err)
	defer func() { os.Stderr = stderr }()
	os.Stderr = f

	res, pending := Pending(
		cfg(),
		WithCacheDir(dir),
		WithCurrentVersion("v1.0.0"),
	)
	require.True(t, pending)
	require.Equal(t, "v1.2.0", res.LatestRef)

	var calls atomic.Int32
	t.Setenv("MYAPP_NO_UPDATE_CHECK", "1")
	res, pending = Pending(
		cfg(),
		WithCacheDir(t.TempDir()),
		WithCurrentVersion("v1.0.0"),
		WithLatestFunc(func(context.Context) (string, error) {
			calls.Add(1)
			return "v9.9.9", nil
		}),
	)
	require.False(t, pending)
	require.Empty(t, res)

	flush := Check(
		cfg(),
		WithCacheDir(t.TempDir()),
		WithCurrentVersion("v1.0.0"),
		WithLatestFunc(func(context.Context) (string, error) {
			calls.Add(1)
			return "v9.9.9", nil
		}),
	)
	flush()
	require.True(t, AwaitRefresh(10*time.Millisecond))
	require.Zero(t, calls.Load())
}

func TestCheckRefreshesWhenHintSuppressedByNoTerminal(t *testing.T) {
	dir := t.TempDir()

	stderr := os.Stderr
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	require.NoError(t, err)
	defer func() { os.Stderr = stderr }()
	os.Stderr = f

	flush := Check(
		cfg(),
		WithCacheDir(dir),
		WithCurrentVersion("v1.0.0"),
		WithLatestFunc(func(context.Context) (string, error) {
			return "v1.2.0", nil
		}),
	)
	flush()
	require.True(t, AwaitRefresh(time.Second))

	info, err := f.Stat()
	require.NoError(t, err)
	require.Zero(t, info.Size())

	res, pending := Pending(cfg(), WithCacheDir(dir), WithCurrentVersion("v1.0.0"))
	require.True(t, pending)
	require.Equal(t, "v1.2.0", res.LatestRef)
}

func TestSkipSuppressesUntilDifferentRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		current    string
		skipped    string
		next       string
		comparator func(string, string) bool
	}{
		{
			name:    "semver",
			current: "v1.0.0",
			skipped: "v1.2.0",
			next:    "v1.3.0",
		},
		{
			name:       "opaque ref",
			current:    "aaaaaaaaaaaa",
			skipped:    "bbbbbbbbbbbb",
			next:       "cccccccccccc",
			comparator: differentRef,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			opts := []Option{WithCacheDir(dir), WithCurrentVersion(tc.current)}
			if tc.comparator != nil {
				opts = append(opts, WithComparator(tc.comparator))
			}
			c := newChecker(cfg(), opts...)
			c.writeStamp(tc.skipped)

			_, pending := Pending(cfg(), opts...)
			require.True(t, pending)

			require.NoError(t, Skip(cfg(), tc.skipped, opts...))
			res, pending := Pending(cfg(), opts...)
			require.False(t, pending)
			require.True(t, res.LatestIsUpdate)

			c.writeStamp(tc.next)
			res, pending = Pending(cfg(), opts...)
			require.True(t, pending)
			require.Equal(t, tc.next, res.LatestRef)
		})
	}
}

func TestOldFormatCacheReadsCleanly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, stampName),
		[]byte("v2.0.0\n"),
		stampPerm,
	))

	c := newChecker(cfg(), WithCacheDir(dir))
	st, _, cached := c.readStamp()
	require.True(t, cached)
	require.Equal(t, "v2.0.0", st.Latest)
	require.Empty(t, st.Skipped)

	res, pending := Pending(cfg(), WithCacheDir(dir), WithCurrentVersion("v1.0.0"))
	require.True(t, pending)
	require.Equal(t, "v2.0.0", res.LatestRef)
}

func TestEmptyCacheIsNotPending(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, stampName), nil, stampPerm))

	res, pending := Pending(cfg(), WithCacheDir(dir), WithCurrentVersion("v1.0.0"))
	require.False(t, pending)
	require.Empty(t, res.LatestRef)
}

func TestUnreadableCacheIsNotPending(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "not-a-directory")
	require.NoError(t, os.WriteFile(cacheDir, []byte("not a directory"), stampPerm))

	_, pending := Pending(
		cfg(),
		WithCacheDir(cacheDir),
		WithCurrentVersion("v1.0.0"),
		WithLatestFunc(func(context.Context) (string, error) {
			return "v1.2.0", nil
		}),
	)
	require.True(t, AwaitRefresh(time.Second))
	require.False(t, pending)
}

func TestCustomComparatorGovernsHintPendingAndResurface(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	opts := []Option{
		WithCacheDir(dir),
		WithCurrentVersion("alpha"),
		WithComparator(differentRef),
	}
	c := newChecker(cfg(), opts...)

	c.writeStamp("alpha")
	res, pending := Pending(cfg(), opts...)
	_, hint := c.shouldHint()
	require.False(t, pending)
	require.False(t, hint)
	require.False(t, res.LatestIsUpdate)

	c.writeStamp("beta")
	res, pending = Pending(cfg(), opts...)
	latest, hint := c.shouldHint()
	require.True(t, pending)
	require.True(t, hint)
	require.Equal(t, latest, res.LatestRef)

	require.NoError(t, Skip(cfg(), "beta", opts...))
	res, pending = Pending(cfg(), opts...)
	_, hint = c.shouldHint()
	require.False(t, pending)
	require.False(t, hint)
	require.True(t, res.LatestIsUpdate)

	c.writeStamp("gamma")
	res, pending = Pending(cfg(), opts...)
	latest, hint = c.shouldHint()
	require.True(t, pending)
	require.True(t, hint)
	require.Equal(t, latest, res.LatestRef)
}

func TestRefDisplayAppliesToPendingAndHintFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	display := func(ref string) string {
		if len(ref) > 7 {
			return ref[:7]
		}
		return ref
	}
	opts := []Option{
		WithCacheDir(dir),
		WithCurrentVersion("111111111111"),
		WithComparator(differentRef),
		WithRefDisplay(display),
	}
	c := newChecker(brew.Config{Formula: "myapp"}, opts...)
	c.writeStamp("222222222222")

	res, pending := Pending(brew.Config{Formula: "myapp"}, opts...)
	require.True(t, pending)
	require.Equal(t, "1111111", res.CurrentDisplay)
	require.Equal(t, "2222222", res.LatestDisplay)

	installed, latest := c.hintRefs(res)
	require.Equal(t, "1111111", installed)
	require.Equal(t, "2222222", latest)
}

func TestTrackSwitchDoesNotCompareOtherTrackCache(t *testing.T) {
	dir := t.TempDir()
	newChecker(
		cfg(),
		WithCacheDir(dir),
		WithCurrentVersion("v1.0.0"),
		WithChannel("stable"),
	).writeStamp("v2.0.0")

	started := make(chan struct{})
	release := make(chan struct{})
	opts := []Option{
		WithCacheDir(dir),
		WithCurrentVersion("v1.0.0"),
		WithChannel("rolling"),
		WithLatestFunc(func(context.Context) (string, error) {
			close(started)
			<-release
			return "v3.0.0", nil
		}),
	}

	res, pending := Pending(cfg(), opts...)
	require.False(t, pending)
	require.Equal(t, "rolling", res.Track)
	require.Empty(t, res.LatestRef)
	<-started

	close(release)
	require.True(t, AwaitRefresh(time.Second))

	res, pending = Pending(cfg(), opts...)
	require.True(t, pending)
	require.Equal(t, "v3.0.0", res.LatestRef)
}

func TestAwaitRefreshNoOpAndTimeout(t *testing.T) {
	require.True(t, AwaitRefresh(time.Millisecond))

	started := make(chan struct{})
	release := make(chan struct{})
	_, _ = Pending(
		cfg(),
		WithCacheDir(t.TempDir()),
		WithCurrentVersion("v1.0.0"),
		WithLatestFunc(func(context.Context) (string, error) {
			close(started)
			<-release
			return "v1.2.0", nil
		}),
	)
	<-started

	start := time.Now()
	require.False(t, AwaitRefresh(20*time.Millisecond))
	require.Less(t, time.Since(start), 200*time.Millisecond)

	close(release)
	require.True(t, AwaitRefresh(time.Second))
}

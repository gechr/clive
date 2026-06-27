package version_test

import (
	"testing"

	"github.com/gechr/clive/version"
	goversion "github.com/hashicorp/go-version"
	"github.com/stretchr/testify/assert"
)

func mustParse(t *testing.T, s string) *goversion.Version {
	t.Helper()
	v, err := goversion.NewVersion(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}

func TestCompare(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b string
		want int
	}{
		{
			name: "equal",
			a:    "1.2.3",
			b:    "1.2.3",
			want: 0,
		},
		{name: "major higher", a: "2.0.0", b: "1.9.9", want: 1},
		{name: "major lower", a: "1.9.9", b: "2.0.0", want: -1},
		{name: "minor higher", a: "1.3.0", b: "1.2.9", want: 1},
		{name: "patch higher", a: "1.2.4", b: "1.2.3", want: 1},
		{name: "release outranks prerelease", a: "1.2.3", b: "1.2.3-rc1", want: 1},
		{name: "prerelease loses to release", a: "1.2.3-rc1", b: "1.2.3", want: -1},
		{name: "rc10 > rc2 (natural)", a: "1.2.3-rc10", b: "1.2.3-rc2", want: 1},
		{name: "rc2 < rc10 (natural)", a: "1.2.3-rc2", b: "1.2.3-rc10", want: -1},
		{name: "equal prereleases", a: "1.2.3-rc1", b: "1.2.3-rc1", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, b := mustParse(t, tt.a), mustParse(t, tt.b)
			assert.Equal(t, tt.want, version.Compare(a, b))
		})
	}
}

func TestCompareNil(t *testing.T) {
	t.Parallel()

	v := mustParse(t, "1.0.0")
	assert.Equal(t, 0, version.Compare(nil, nil))
	assert.Equal(t, -1, version.Compare(nil, v))
	assert.Equal(t, 1, version.Compare(v, nil))
}

func TestGreaterThanLessThanEqual(t *testing.T) {
	t.Parallel()

	a := mustParse(t, "1.2.3")
	b := mustParse(t, "1.2.4")

	assert.True(t, version.GreaterThan(b, a))
	assert.False(t, version.GreaterThan(a, b))
	assert.True(t, version.LessThan(a, b))
	assert.False(t, version.LessThan(b, a))
	assert.True(t, version.Equal(a, mustParse(t, "1.2.3")))
	assert.False(t, version.Equal(a, b))
}

func TestEqualString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "identical", a: "1.2.0", b: "1.2.0", want: true},
		{name: "prefix on one side", a: "v1.2.0", b: "1.2.0", want: true},
		{name: "prefix on both sides", a: "v1.2.0", b: "v1.2.0", want: true},
		{name: "missing trailing segment", a: "v1.2", b: "1.2.0", want: true},
		{name: "different patch", a: "1.2.0", b: "1.2.1", want: false},
		{name: "prerelease differs", a: "1.2.0-rc1", b: "1.2.0-rc2", want: false},
		{name: "non-semver equal fallback", a: "stable", b: "stable", want: true},
		{name: "non-semver differs fallback", a: "stable", b: "edge", want: false},
		{name: "non-semver prefix fallback", a: "vstable", b: "stable", want: true},
		{name: "both empty", a: "", b: "", want: true},
		{name: "empty vs set", a: "", b: "1.2.0", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, version.EqualString(tt.a, tt.b))
		})
	}
}

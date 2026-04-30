package semver_test

import (
	"testing"

	mmsemver "github.com/Masterminds/semver/v3"
	"github.com/gechr/clive/semver"
	"github.com/stretchr/testify/assert"
)

func mustParse(t *testing.T, s string) *mmsemver.Version {
	t.Helper()
	v, err := mmsemver.NewVersion(s)
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
		{name: "equal", a: "1.2.3", b: "1.2.3", want: 0},
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
			assert.Equal(t, tt.want, semver.Compare(a, b))
		})
	}
}

func TestCompareNil(t *testing.T) {
	t.Parallel()

	v := mustParse(t, "1.0.0")
	assert.Equal(t, 0, semver.Compare(nil, nil))
	assert.Equal(t, -1, semver.Compare(nil, v))
	assert.Equal(t, 1, semver.Compare(v, nil))
}

func TestGreaterThanLessThanEqual(t *testing.T) {
	t.Parallel()

	a := mustParse(t, "1.2.3")
	b := mustParse(t, "1.2.4")

	assert.True(t, semver.GreaterThan(b, a))
	assert.False(t, semver.GreaterThan(a, b))
	assert.True(t, semver.LessThan(a, b))
	assert.False(t, semver.LessThan(b, a))
	assert.True(t, semver.Equal(a, mustParse(t, "1.2.3")))
	assert.False(t, semver.Equal(a, b))
}

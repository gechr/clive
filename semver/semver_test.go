package semver_test

import (
	"testing"

	"github.com/gechr/clive/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHasPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want bool
	}{
		{"v1.2.3", true},
		{"V1.2.3", true},
		{"1.2.3", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, semver.HasPrefix(tt.in))
		})
	}
}

func TestAddPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in, want string
	}{
		{"1.2.3", "v1.2.3"},
		{"v1.2.3", "v1.2.3"},
		{"V1.2.3", "V1.2.3"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, semver.AddPrefix(tt.in))
		})
	}
}

func TestRemovePrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in, want string
	}{
		{"v1.2.3", "1.2.3"},
		{"V1.2.3", "1.2.3"},
		{"1.2.3", "1.2.3"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, semver.RemovePrefix(tt.in))
		})
	}
}

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "plain semver", in: "1.2.3", want: "1.2.3"},
		{name: "with v prefix", in: "v1.2.3", want: "1.2.3"},
		{name: "two parts padded", in: "1.2", want: "1.2.0"},
		{name: "one part padded", in: "1", want: "1.0.0"},
		// "-g..." is stripped, leaving "0.20.8-2" which semver treats as
		// a "2" prerelease - matching the underlying Masterminds behaviour.
		{name: "git describe trimmed", in: "0.20.8-2-g55ae225", want: "0.20.8-2"},
		{name: "v + git describe", in: "v0.20.8-2-g55ae225", want: "0.20.8-2"},
		{name: "invalid", in: "not-a-version", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := semver.Parse(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got.String())
		})
	}
}

func TestIsDev(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		// git describe format
		{name: "git describe", in: "0.20.8-2-g55ae225", want: true},
		{name: "git describe with v", in: "v0.20.8-2-g55ae225", want: true},
		{name: "double-digit count", in: "1.2.3-10-gabcdef0", want: true},
		{name: "triple-digit count", in: "1.0.0-123-g1234567", want: true},
		{name: "based on rc tag", in: "1.0.0-rc1-5-g1234567", want: true},
		{name: "based on beta tag", in: "2.0.0-beta.1-3-gabc1234", want: true},

		// -dev suffix format
		{name: "dev suffix", in: "0.21.3-3b71351-dev", want: true},
		{name: "dev suffix with v", in: "v0.21.3-3b71351-dev", want: true},
		{name: "new dev format with count", in: "0.21.3-1-gabcdef1-dev", want: true},

		// not dev
		{name: "plain semver", in: "0.20.8", want: false},
		{name: "v + plain", in: "v1.0.0", want: false},
		{name: "prerelease", in: "1.0.0-beta.1", want: false},
		{name: "prerelease rc", in: "1.0.0-rc1", want: false},
		{name: "build metadata", in: "1.0.0+build", want: false},
		{name: "empty", in: "", want: false},
		{name: "just digits", in: "123", want: false},
		{name: "-g but no count", in: "1.0.0-gtest", want: false},
		{name: "non-numeric count", in: "1.0.0-abc-g1234567", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, semver.IsDev(tt.in))
		})
	}
}

func TestExtractBase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		// git describe
		{name: "git describe", in: "0.20.8-2-g55ae225", want: "0.20.8"},
		{name: "git describe with v", in: "v0.20.8-2-g55ae225", want: "0.20.8"},
		{name: "double-digit count", in: "1.2.3-10-gabcdef0", want: "1.2.3"},
		{name: "based on rc", in: "1.0.0-rc1-5-g1234567", want: "1.0.0-rc1"},
		{name: "based on beta", in: "2.0.0-beta.1-3-gabc1234", want: "2.0.0-beta.1"},

		// -dev suffix (old, no count)
		{name: "dev suffix", in: "0.21.3-3b71351-dev", want: "0.21.3"},
		{name: "dev suffix with v", in: "v0.21.3-3b71351-dev", want: "0.21.3"},

		// new dev (with count, preserves base)
		{name: "new dev with count", in: "0.21.3-1-gabcdef1-dev", want: "0.21.3"},
		{name: "new dev with v", in: "v0.21.3-1-gabcdef1-dev", want: "0.21.3"},
		{name: "new dev double-digit", in: "1.2.3-10-gabcdef0-dev", want: "1.2.3"},

		// non-dev
		{name: "plain", in: "0.20.8", want: "0.20.8"},
		{name: "with v", in: "v1.0.0", want: "1.0.0"},
		{name: "prerelease", in: "1.0.0-beta.1", want: "1.0.0-beta.1"},
		{name: "empty", in: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, semver.ExtractBase(tt.in))
		})
	}
}

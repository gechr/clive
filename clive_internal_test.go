package clive

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInfoRepo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		info Info
		want string
	}{
		{
			name: "explicit Repo wins",
			info: Info{Module: "github.com/x/y", Repo: "z/w"},
			want: "z/w",
		},
		{
			name: "derived from github module",
			info: Info{Module: "github.com/gechr/clone"},
			want: "gechr/clone",
		},
		{name: "non-github module", info: Info{Module: "go.example.com/foo"}, want: ""},
		{name: "zero value", info: Info{}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.info.repo())
		})
	}
}

func TestFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in, want string
	}{
		{"1.2.3", "v1.2.3"},
		{"v1.2.3", "v1.2.3"},
		{"1.2.3-", "v1.2.3"},
		{"0.21.4-1-g4bed8a3-dev", "v0.21.4-1-g4bed8a3-dev"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, format(tt.in))
		})
	}
}

func TestExtractCommitHash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "git describe", in: "v0.21.4-1-g4bed8a31", want: "4bed8a31"},
		{name: "git describe + dev", in: "v0.21.4-1-g4bed8a31-dev", want: "4bed8a31"},
		{name: "old dev format", in: "v0.21.4-4bed8a31-dev", want: "4bed8a31"},
		{name: "plain release no hash", in: "v1.2.3", want: ""},
		{name: "empty", in: "", want: ""},
		{name: "non-hex after -g", in: "v1.0.0-1-gnothex0", want: ""},
		{name: "too-short hash", in: "v1.0.0-1-gabc", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, extractCommitHash(tt.in))
		})
	}
}

func TestIsHex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want bool
	}{
		{"abcdef0", true},
		{"1234567", true},
		{"ABCDEF0", true},
		{"abc", false},     // too short
		{"abcdefg", false}, // 'g' not hex
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isHex(tt.in))
		})
	}
}

func TestIsNewer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{name: "newer available", current: "v1.0.0", latest: "v1.1.0", want: true},
		{name: "same version", current: "v1.0.0", latest: "v1.0.0", want: false},
		{name: "current is newer", current: "v1.1.0", latest: "v1.0.0", want: false},
		{
			name:    "dev current, release latest",
			current: "v1.0.0-g4bed8a3-dev",
			latest:  "v1.0.0",
			want:    true,
		},
		{name: "unparseable current", current: "not-a-version", latest: "v1.0.0", want: false},
		{name: "unparseable latest", current: "v1.0.0", latest: "not-a-version", want: false},
		{name: "both empty", current: "", latest: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isNewer(tt.current, tt.latest))
		})
	}
}

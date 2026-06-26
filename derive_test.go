package clive_test

import (
	"testing"

	"github.com/gechr/clive"
	"github.com/stretchr/testify/require"
)

func TestDeriveVersion(t *testing.T) {
	tests := []struct {
		name     string
		module   string
		revision string
		want     string
	}{
		{
			name:   "tagged release",
			module: "v1.2.3",
			want:   "v1.2.3",
		},
		{name: "release without v prefix", module: "1.2.3", want: "v1.2.3"},
		{
			name:   "go install pseudo-version",
			module: "v0.0.0-20240101000000-abcdef123456",
			want:   "v0.0.0-gabcdef1-dev",
		},
		{
			name:     "devel falls back to vcs revision",
			module:   "(devel)",
			revision: "abcdef1234567890deadbeef",
			want:     "v0.0.0-gabcdef1-dev",
		},
		{
			name:     "module preferred over revision",
			module:   "v2.0.0",
			revision: "deadbeef",
			want:     "v2.0.0",
		},
		{name: "no metadata", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, clive.DeriveVersion(tc.module, tc.revision))
		})
	}
}

package updater

import (
	"os"
	"path/filepath"

	xfilepath "github.com/gechr/x/filepath"
)

// defaultBinDir is the install directory when a Config leaves it unset, relative
// to the user's home. ~/.local/bin is the conventional per-user binary directory
// and is usually on PATH, unlike $GOPATH/bin.
const defaultBinDir = ".local/bin"

// InstallDir resolves the directory a binary is installed into: dir when set
// (with a leading "~/" and any env vars expanded), else ~/.local/bin. It returns
// "" only when no dir was given and the home directory cannot be resolved,
// letting a caller fall back to a mechanism-specific default.
func InstallDir(dir string) string {
	if dir != "" {
		return xfilepath.Expand(dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, defaultBinDir)
}

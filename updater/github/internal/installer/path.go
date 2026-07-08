package installer

import (
	"os"

	xfilepath "github.com/gechr/x/filepath"
)

// executablePath returns the running executable's path with all symlinks resolved.
func executablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := xfilepath.Resolve(exe)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

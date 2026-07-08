//go:build !windows

package installer

func hideFile(_ string) error {
	return nil
}

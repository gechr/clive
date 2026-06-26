package updater

import "cmp"

// DisplayName resolves the human-facing name shown in messages: name when set,
// else the binary name. Shared by every mechanism's Config.DisplayName.
func DisplayName(name, binary string) string { return cmp.Or(name, binary) }

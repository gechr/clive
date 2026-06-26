package updater

import "strings"

// ProxyBypass returns env entries that disable any inherited proxy for an update
// subprocess: each proxy variable is blanked (an empty value overrides the
// inherited one) and NO_PROXY is set to "*" so every host is exempt. Both the
// upper- and lower-case spellings are set, as tools read either.
func ProxyBypass() []string {
	return []string{
		"HTTP_PROXY=", "http_proxy=",
		"HTTPS_PROXY=", "https_proxy=",
		"ALL_PROXY=", "all_proxy=",
		"NO_PROXY=*", "no_proxy=*",
	}
}

// GoPrivate returns a GOPRIVATE value that includes module, preserving any
// existing entries so the caller's configuration is not discarded. Both inputs
// are trimmed so surrounding whitespace never yields a malformed list.
func GoPrivate(module, existing string) string {
	module = strings.TrimSpace(module)
	existing = strings.TrimSpace(existing)
	if existing == "" {
		return module
	}
	return module + "," + existing
}

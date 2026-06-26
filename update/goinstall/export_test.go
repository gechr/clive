package goinstall

// Exposed for black-box tests of the package's pure helpers.

var ModuleVersion = moduleVersion

var ProxyBypass = proxyBypass

func (c Config) InstallTarget(channel Channel) string { return c.installTarget(channel) }

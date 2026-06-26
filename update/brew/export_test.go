package brew

// Exposed for black-box tests of the package's pure helpers.

var HeadBuild = headBuild

var LinkedKeg = linkedKeg

var ProxyBypass = proxyBypass

func (c Config) FormulaRef() string { return c.formulaRef() }

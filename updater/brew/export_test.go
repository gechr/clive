package brew

// Exposed for black-box tests of the package's pure helpers.

var HeadBuild = headBuild

func (c Config) FormulaRef() string { return c.formulaRef() }

func (c Config) ResolveVersionArg() string { return c.versionArg() }

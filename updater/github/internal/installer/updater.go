package installer

import (
	"fmt"
	"regexp"
	"runtime"
)

// Updater is responsible for managing the context of self-update.
type Updater struct {
	source        Source
	validator     Validator
	filters       []*regexp.Regexp
	os            string
	arch          string
	arm           uint8
	universalArch string // only filled in when needed
	prerelease    bool
	draft         bool
	oldSavePath   string
}

// NewUpdater creates a new updater instance.
// If you don't specify a source in the config object, GitHub will be used
func NewUpdater(config Config) (*Updater, error) {
	source := config.Source
	if source == nil {
		// default source is GitHub
		// an error can only be returned when using GitHub Enterprise URLs
		source, _ = NewGitHubSource(GitHubConfig{})
	}

	filtersRe := make([]*regexp.Regexp, 0, len(config.Filters))
	for _, filter := range config.Filters {
		re, err := regexp.Compile(filter)
		if err != nil {
			return nil, fmt.Errorf(
				"could not compile regular expression %q for filtering releases: %w",
				filter,
				err,
			)
		}
		filtersRe = append(filtersRe, re)
	}

	os := config.OS
	if os == "" {
		os = runtime.GOOS
	}
	arch := config.Arch
	if arch == "" {
		arch = runtime.GOARCH
	}
	arm := config.Arm
	if arm == 0 && arch == "arm" {
		exe, _ := GetExecutablePath()
		arm = getGOARM(exe)
	}
	universalArch := ""
	if os == "darwin" && config.UniversalArch != "" {
		universalArch = config.UniversalArch
	}

	return &Updater{
		source:        source,
		validator:     config.Validator,
		filters:       filtersRe,
		os:            os,
		arch:          arch,
		arm:           arm,
		universalArch: universalArch,
		prerelease:    config.Prerelease,
		draft:         config.Draft,
		oldSavePath:   config.OldSavePath,
	}, nil
}

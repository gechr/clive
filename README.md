# clive

Version detection, display, and update-check helpers for Go CLI binaries.

## Packages

| Package        | Description                                                          |
| -------------- | -------------------------------------------------------------------- |
| `clive`        | `Info`, `Current`, `Print`, `PrintDetailed`, `VersionLink`, `Latest` |
| `clive/semver` | Version parsing, dev-build detection, natural-sort comparison        |

## Installation

```text
go get github.com/gechr/clive
```

## Usage

### Inject build metadata via `-ldflags`

```make
VERSION   ?= $(shell git describe --tags 2>/dev/null || echo 0.0.0-dev)
BUILDTIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

GO_LDFLAGS := -s -w \
  -X github.com/gechr/clive.version=$(VERSION) \
  -X github.com/gechr/clive.buildTime=$(BUILDTIME)
```

When ldflags are not set (e.g. `go install ...@latest`), `clive.Current` falls
back to `debug.BuildInfo` so the binary still reports a sensible version.

### Print the version

```go
import "github.com/gechr/clive"

func main() {
    if printVersion {
        clive.Print()
        return
    }
}
```

### Detailed version output with hyperlinks

```go
info := clive.Info{Module: "github.com/gechr/myapp"}
info.PrintDetailed()
```

```text
Version:     v0.1.2
Go version:  go1.26.2
OS/Arch:     darwin/arm64
Built:       2026-04-29T18:00:00Z (12 hours ago)
Commit:      a1b2c3d4...
```

`Repo` is auto-derived from `Module` when the module path starts with
`github.com/`. Override explicitly when it doesn't:

```go
info := clive.Info{
    Module: "go.example.com/myapp",
    Repo:   "myorg/myapp",
}
```

### Check the Go module proxy for the latest version

```go
latest, err := info.Latest(ctx)
```

### Compare versions

```go
import "github.com/gechr/clive/semver"

if semver.IsDev(clive.Current()) {
    base := semver.ExtractBase(clive.Current())
    // ...
}

a, _ := semver.Parse("v1.2.3")
b, _ := semver.Parse("v1.2.4")
semver.GreaterThan(b, a) // true
```

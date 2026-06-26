# clive

Version detection, display, and update-check helpers for Go CLIs.

## Packages

| Package                   | Description                                                    |
| ------------------------- | -------------------------------------------------------------- |
| `clive`                   | Version detection, display, and update checks                  |
| `clive/semver`            | [Semantic version](https://semver.org/) parsing and comparison |
| `clive/updater`           | Shared self-update interface and helpers                       |
| `clive/updater/brew`      | Self-update a CLI binary through Homebrew                      |
| `clive/updater/goinstall` | Self-update a CLI binary through `go install`                  |
| `clive/updater/github`    | Self-update a CLI binary from GitHub release assets            |
| `clive/notify`            | Background update hints                                        |

## Installation

```text
go get github.com/gechr/clive
```

## Usage

### Inject build metadata via `-ldflags`

```bash
VERSION=$(git describe --tags)
BUILDTIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)

go build -ldflags "-s -w \
  -X github.com/gechr/clive.version=${VERSION} \
  -X github.com/gechr/clive.buildTime=${BUILDTIME}" ./...
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

### Check for updates

```go
info := clive.Info{Module: "github.com/gechr/myapp"}
if ok, _ := info.UpdateAvailable(ctx); ok {
    fmt.Printf("A new version is available. Run: go install %s@latest\n", info.Module)
}
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

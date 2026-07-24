// Package version exposes build metadata that is stamped in at link time.
//
// The values are set with -ldflags by the Makefile, e.g.
//
//	-X github.com/vanboven073/BeerMate-DisplayManager/internal/version.Version=1.0.0
//
// When built without stamping (go run, go test) the fallbacks below apply so the
// application always reports something sensible.
package version

import (
	"runtime"
	"runtime/debug"
	"sync"
)

var (
	// Version is the semantic release version.
	Version = "0.0.0-dev"
	// Commit is the short git SHA the binary was built from.
	Commit = "unknown"
	// BuildDate is an RFC3339 timestamp of the build.
	BuildDate = "unknown"
)

// Info is the immutable build description reported by /health and the admin UI.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	Go        string `json:"go"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

var (
	once   sync.Once
	cached Info
)

// Get returns the build information for this binary.
func Get() Info {
	once.Do(func() {
		commit := Commit
		if commit == "unknown" {
			// Fall back to VCS stamping that the Go toolchain embeds automatically.
			if bi, ok := debug.ReadBuildInfo(); ok {
				for _, s := range bi.Settings {
					if s.Key == "vcs.revision" && s.Value != "" {
						if len(s.Value) > 12 {
							commit = s.Value[:12]
						} else {
							commit = s.Value
						}
					}
				}
			}
		}
		cached = Info{
			Version:   Version,
			Commit:    commit,
			BuildDate: BuildDate,
			Go:        runtime.Version(),
			OS:        runtime.GOOS,
			Arch:      runtime.GOARCH,
		}
	})
	return cached
}

// String renders a one-line human readable description.
func String() string {
	i := Get()
	return "BeerMate Display Manager " + i.Version + " (" + i.Commit + ", " + i.Go + ", " + i.OS + "/" + i.Arch + ")"
}

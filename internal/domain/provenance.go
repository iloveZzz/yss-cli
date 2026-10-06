package domain

import "runtime/debug"

// Release builders inject both values from the frozen CLI checkout. Normal Go
// builds use VCS build information; absent information is never called committed.
var BuildCommit string
var BuildSourceState string

type Provenance struct {
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	SourceState string `json:"sourceState"`
}

func BuildProvenance() Provenance {
	p := Provenance{Version: Version, Commit: BuildCommit, SourceState: BuildSourceState}
	if p.SourceState == "" {
		p.SourceState = "unknown"
	}
	if p.Commit != "" {
		return p
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		modified := ""
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				p.Commit = s.Value
			case "vcs.modified":
				modified = s.Value
			}
		}
		if p.Commit != "" {
			if modified == "false" {
				p.SourceState = "committed"
			} else {
				p.SourceState = "working-tree"
			}
		}
	}
	return p
}

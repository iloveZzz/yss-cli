package scaffold

import (
	"os"
	"time"
)

// RenderContext captures the generation environment once.
type RenderContext struct {
	Author      string `json:"author"`
	Date        string `json:"date"`
	GeneratedAt string `json:"generatedAt"`
}

func captureRenderContext() *RenderContext {
	author := os.Getenv("USER")
	if author == "" {
		author = "yss-team"
	}
	now := time.Now().UTC()
	return &RenderContext{Author: author, Date: now.Format("2006-01-02"), GeneratedAt: now.Format("2006-01-02T15:04:05.000Z")}
}

type BundleBinding struct {
	Profile         string `json:"profile"`
	SchemaVersion   int    `json:"schemaVersion"`
	BundleHash      string `json:"bundleHash"`
	TemplateVersion string `json:"templateVersion"`
	TemplateCommit  string `json:"templateCommit"`
	SourceState     string `json:"sourceState"`
	SnapshotHash    string `json:"snapshotHash"`
	ManifestHash    string `json:"manifestHash"`
}

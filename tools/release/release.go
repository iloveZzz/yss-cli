// Package release assembles verified native artifacts without compiling or publishing.
package release

type FileRef struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type BundleIdentity struct {
	TemplateVersion    string `json:"templateVersion"`
	TemplateCommit     string `json:"templateCommit"`
	SourceState        string `json:"sourceState"`
	SourceSnapshotHash string `json:"sourceSnapshotHash"`
	ManifestHash       string `json:"manifestHash"`
	BundleHash         string `json:"bundleHash"`
}
type Identity struct {
	CLIVersion       string                    `json:"cliVersion"`
	ProtocolVersion  int                       `json:"protocolVersion"`
	CLICommit        string                    `json:"cliCommit"`
	SourceState      string                    `json:"sourceState"`
	SourceLockSHA256 string                    `json:"sourceLockSha256"`
	Bundles          map[string]BundleIdentity `json:"bundles"`
}
type Artifact struct {
	Platform string  `json:"platform"`
	Binary   FileRef `json:"binary"`
	Receipt  FileRef `json:"receipt"`
}
type Input struct {
	SchemaVersion int `json:"schemaVersion"`
	Identity
	Documents   map[string]FileRef `json:"documents"`
	Artifacts   []Artifact         `json:"artifacts"`
	ReleaseGate FileRef            `json:"releaseGate"`
}
type Check struct {
	Command    []string `json:"command,omitempty"`
	Signal     any      `json:"signal"`
	DurationMS int64    `json:"durationMs,omitempty"`
	ID         string   `json:"id"`
	Status     string   `json:"status"`
	ExitCode   *int     `json:"exitCode"`
	InputDrift *bool    `json:"inputDrift"`
	Unexecuted []string `json:"unexecuted"`
	Report     FileRef  `json:"report"`
	Stdout     FileRef  `json:"stdout"`
	Stderr     FileRef  `json:"stderr"`
}
type Receipt struct {
	GitHubRunID      string  `json:"githubRunId,omitempty"`
	GitHubRunAttempt string  `json:"githubRunAttempt,omitempty"`
	VersionCommand   *Check  `json:"versionCommand,omitempty"`
	BundleCommands   []Check `json:"bundleCommands,omitempty"`
	Error            string  `json:"error,omitempty"`
	SchemaVersion    int     `json:"schemaVersion"`
	Identity
	Platform            string   `json:"platform,omitempty"`
	RuntimePlatform     string   `json:"runtimePlatform,omitempty"`
	RuntimeVerification string   `json:"runtimeVerification,omitempty"`
	BinarySHA256        string   `json:"binarySha256,omitempty"`
	BinaryBeforeSHA256  string   `json:"binaryBeforeSha256,omitempty"`
	BinaryAfterSHA256   string   `json:"binaryAfterSha256,omitempty"`
	Version             FileRef  `json:"version,omitempty"`
	Status              string   `json:"status"`
	ExitCode            *int     `json:"exitCode"`
	InputDrift          *bool    `json:"inputDrift"`
	Unexecuted          []string `json:"unexecuted"`
	Checks              []Check  `json:"checks"`
}

// Expected binds the assembler to its caller's frozen source, not the input's claims.
type Expected struct {
	Identity
	Documents      map[string][]byte
	RepositoryRoot string
}
type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string { return e.Code + ": " + e.Detail }
func Code(err error) string {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return "INTERNAL"
}

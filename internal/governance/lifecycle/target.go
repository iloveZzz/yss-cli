package lifecycle

type ProgressionConsumer struct {
	Profile       string `json:"profile"`
	Root          string `json:"root"`
	CheckpointRef string `json:"checkpoint_ref"`
}

type ProgressionTarget struct {
	SchemaVersion int                   `json:"schema_version"`
	Kind          string                `json:"kind"`
	FeatureID     string                `json:"feature_id"`
	CheckpointRef string                `json:"checkpoint_ref"`
	Target        string                `json:"target"`
	IntentSource  string                `json:"intent_source"`
	Consumers     []ProgressionConsumer `json:"consumers"`
}

type BackendAuthorization struct{ Mode, CheckpointRef, TerminalRef string }

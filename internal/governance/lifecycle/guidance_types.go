package lifecycle

type ProfileRecommendation struct {
	Profile           string     `json:"profile"`
	Recommendation    string     `json:"recommendation"`
	Basis             string     `json:"basis"`
	TargetRoot        string     `json:"target_root,omitempty"`
	EngineeringStatus string     `json:"engineering_status"`
	InputStatus       string     `json:"input_status"`
	Missing           []string   `json:"missing"`
	Commands          [][]string `json:"commands"`
}

type ProfileGuidanceResult struct {
	SchemaVersion    int                     `json:"schema_version"`
	Status           string                  `json:"status"`
	Reason           string                  `json:"reason,omitempty"`
	ReadOnly         bool                    `json:"read_only"`
	ExecutionAllowed bool                    `json:"execution_allowed"`
	Profile          string                  `json:"profile,omitempty"`
	Default          string                  `json:"default,omitempty"`
	DefaultCommands  [][]string              `json:"default_commands"`
	Recommendations  []ProfileRecommendation `json:"recommendations"`
	Missing          []string                `json:"missing"`
}

package kernel

type Term struct {
	TermRef           string   `json:"term_ref"`
	Term              string   `json:"term"`
	Meaning           string   `json:"meaning"`
	EnglishIdentifier string   `json:"english_identifier"`
	ContextID         string   `json:"context_id"`
	ForbiddenAliases  []string `json:"forbidden_aliases"`
	Notes             string   `json:"notes"`
	Line              int      `json:"line"`
}

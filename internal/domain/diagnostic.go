package domain

// ErrorDetail is supplied at the failing boundary. Presentation never guesses
// a cause by matching localized error messages.
type ErrorDetail struct {
	ID      string
	Cause   string
	Context map[string]any
}

type explainedError struct {
	err    error
	detail ErrorDetail
}

func (e *explainedError) Error() string                 { return e.err.Error() }
func (e *explainedError) Unwrap() error                 { return e.err }
func (e *explainedError) DiagnosticDetail() ErrorDetail { return e.detail }
func Explain(err error, id, cause string, context map[string]any) error {
	if err == nil {
		return nil
	}
	return &explainedError{err, ErrorDetail{id, cause, context}}
}

type wrappedError struct {
	base  *Error
	cause error
}

type messageError struct {
	err     error
	message string
}

func (e *messageError) Error() string             { return e.message }
func (e *messageError) Unwrap() error             { return e.err }
func WithMessage(err error, message string) error { return &messageError{err, message} }

func (e *wrappedError) Error() string { return e.base.Error() }
func (e *wrappedError) Unwrap() error { return e.cause }
func (e *wrappedError) As(target any) bool {
	if p, ok := target.(**Error); ok {
		*p = e.base
		return true
	}
	return false
}

type DiagnosticStep struct {
	Effect      string   `json:"effect"`
	Description string   `json:"description"`
	Argv        []string `json:"argv,omitempty"`
	Condition   string   `json:"condition,omitempty"`
}
type Diagnostic struct {
	SchemaVersion  int              `json:"schemaVersion"`
	ID             string           `json:"id"`
	Code           string           `json:"code"`
	Summary        string           `json:"summary"`
	Cause          string           `json:"cause"`
	OriginalCause  string           `json:"originalCause,omitempty"`
	Context        map[string]any   `json:"context"`
	PossibleCauses []string         `json:"possibleCauses,omitempty"`
	Unverified     []string         `json:"unverified,omitempty"`
	Examples       []string         `json:"examples,omitempty"`
	Steps          []DiagnosticStep `json:"steps"`
	Recheck        []DiagnosticStep `json:"recheck"`
	Help           string           `json:"help"`
	Version        map[string]any   `json:"version,omitempty"`
	Report         any              `json:"report,omitempty"`
}

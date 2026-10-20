package tracker

import "errors"

// syncFailure retains side effects at the CLI ErrorResult boundary.
type syncFailure struct {
	cause  error
	result map[string]any
}

func (e *syncFailure) Error() string    { return e.cause.Error() }
func (e *syncFailure) Unwrap() error    { return e.cause }
func (e *syncFailure) ErrorResult() any { return e.result }

func failedSync(result map[string]any, err error) (any, error) {
	result["message"] = err.Error()
	result["next_steps"] = []string{"运行 yss tracker status --root <项目> --fetch --json 对账，然后重新生成同步计划；不要直接重放旧计划"}
	return result, &syncFailure{cause: err, result: result}
}

// An ambiguous write may have reached GitHub; never retry it automatically.
type uncertainWrite struct{ error }

func (e *uncertainWrite) Unwrap() error { return e.error }
func writeUnknown(err error) bool {
	var uncertain *uncertainWrite
	return errors.As(err, &uncertain)
}

func remoteState(changed bool) string {
	if changed {
		return "changed"
	}
	return "unchanged"
}

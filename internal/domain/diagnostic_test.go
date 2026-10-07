package domain

import (
	"errors"
	"os"
	"testing"
)

func TestDiagnosticWrappingPreservesOriginalErrorAndPublicCode(t *testing.T) {
	original := &os.PathError{Op: "open", Path: "input file", Err: os.ErrNotExist}
	err := WithMessage(Explain(Wrap("INPUT", original), "INPUT_FILE_NOT_FOUND", "输入缺失", map[string]any{"path": original.Path}), "展示说明")
	var public *Error
	var path *os.PathError
	var detail interface{ DiagnosticDetail() ErrorDetail }
	if !errors.As(err, &public) || public.Code != "INPUT" || !errors.As(err, &path) || path != original || !errors.Is(err, os.ErrNotExist) || !errors.As(err, &detail) {
		t.Fatal("诊断丢失错误分类或原始链")
	}
}

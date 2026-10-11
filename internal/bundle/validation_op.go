package bundle

import (
	"encoding/base64"
	"fmt"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

// A validationOp belongs to one public operation. Its key includes the actual
// encoded content, so a repeated declaration cannot authorize different bytes.
// Retain facts, not decoded buffers; metadata and paths are always rechecked.
type validationOp struct{ checked map[contentKey]contentFacts }
type contentKey struct{ digest, data string }
type contentFacts struct{ size int }

func newValidationOp() *validationOp {
	return &validationOp{checked: make(map[contentKey]contentFacts)}
}

func (op *validationOp) content(f File) (contentFacts, error) {
	key := contentKey{f.Digest, f.Data}
	if facts, ok := op.checked[key]; ok {
		return facts, nil
	}
	data, err := base64.StdEncoding.DecodeString(f.Data)
	if err != nil || safefs.Digest(data) != f.Digest {
		return contentFacts{}, fmt.Errorf("invalid content digest")
	}
	facts := contentFacts{size: len(data)}
	op.checked[key] = facts
	return facts, nil
}

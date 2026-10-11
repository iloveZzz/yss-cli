package bundle

import (
	"context"
	"io"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

// MaxSnapshotBytes includes the newline in an exported public JSON snapshot.
// The private compressed archive has separate compressed/decompressed limits.
const MaxSnapshotBytes = 128 * 1024 * 1024

func writeSnapshot(ctx context.Context, out io.Writer, b *Bundle) error {
	limited := &snapshotWriter{ctx: ctx, out: out, remaining: MaxSnapshotBytes}
	if err := writeBundleJSON(limited, b); err != nil {
		return err
	}
	_, err := limited.Write([]byte{'\n'})
	return err
}

type snapshotWriter struct {
	ctx       context.Context
	out       io.Writer
	remaining int64
}

func (w *snapshotWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(p)) > w.remaining {
		return 0, domain.Fail("BUNDLE", "公开快照超过 128 MiB")
	}
	n, err := w.out.Write(p)
	w.remaining -= int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

func readSnapshotData(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxSnapshotBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxSnapshotBytes {
		return nil, domain.Fail("BASE_BUNDLE", "旧 Bundle 必须是最多 128 MiB 的普通 JSON 文件")
	}
	return raw, nil
}

package transaction

import (
	"os"
	"sync/atomic"
)

type temporaryWriter struct {
	write func(*os.File, []byte) (int, error)
}

// The default path is the real file write. Only the test build supplies an
// installer for observing a producer before Chmod or publication. Atomic
// publication keeps the private seam safe when unrelated writes run in parallel.
var temporaryWriterOverride atomic.Pointer[temporaryWriter]

func writeTemporary(f *os.File, b []byte) (int, error) {
	if writer := temporaryWriterOverride.Load(); writer != nil {
		return writer.write(f, b)
	}
	return f.Write(b)
}

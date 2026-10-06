package transaction

import "os"

// SetTemporaryWriterForTest exists only in the test binary. Crash helpers install
// it before Apply and still perform real writes through the supplied file.
func SetTemporaryWriterForTest(write func(*os.File, []byte) (int, error)) func() {
	previous := temporaryWriterOverride.Swap(&temporaryWriter{write: write})
	return func() { temporaryWriterOverride.Store(previous) }
}

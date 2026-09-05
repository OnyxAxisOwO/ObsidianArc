package chat

import (
	"bytes"
	"io"
)

// http.ServeContent needs an io.ReadSeeker so it can answer a Range request;
// bytes.Reader is exactly that, and this names why it is here.
func newReaderAt(data []byte) io.ReadSeeker { return bytes.NewReader(data) }

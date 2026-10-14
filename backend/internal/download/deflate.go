package download

import (
	"compress/flate"
	"io"
)

// fastDeflate is the level-1 compressor registered for Method deflate.
func fastDeflate(w io.Writer) (io.WriteCloser, error) {
	return flate.NewWriter(w, flate.BestSpeed)
}

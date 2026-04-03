package httputil

import (
	"compress/gzip"
	"io"
	"sync"
)

// gzipPool reuses gzip.Reader instances to avoid the ~54 KB/op
// allocation cost of gzip.NewReader on every compressed request.
var gzipPool sync.Pool

// GetGzipReader returns a pooled gzip.Reader reset to r, or creates a new one.
// On error (invalid gzip header), returns nil and the error.
func GetGzipReader(r io.Reader) (*gzip.Reader, error) {
	if v, ok := gzipPool.Get().(*gzip.Reader); ok {
		if err := v.Reset(r); err != nil {
			gzipPool.Put(v)
			return nil, err
		}
		return v, nil
	}
	return gzip.NewReader(r)
}

// PutGzipReader returns a gzip.Reader to the pool after closing it.
func PutGzipReader(gr *gzip.Reader) {
	gr.Close()
	gzipPool.Put(gr)
}

package middleware

import (
	"compress/flate"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"

	"urgentry/internal/httputil"
)

// gzipReaderPool reuses gzip.Reader instances to avoid the ~54 KB/op
// allocation cost of gzip.NewReader on every compressed request.
var gzipReaderPool sync.Pool

// Decompress is HTTP middleware that transparently decompresses request bodies
// based on the Content-Encoding header. Supports gzip and deflate.
func Decompress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		encoding := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding")))

		switch encoding {
		case "gzip":
			var gr *gzip.Reader
			if v, ok := gzipReaderPool.Get().(*gzip.Reader); ok {
				if err := v.Reset(r.Body); err != nil {
					gzipReaderPool.Put(v)
					httputil.WriteAPIError(w, httputil.APIError{
						Status: http.StatusBadRequest,
						Code:   "invalid_gzip",
						Detail: "invalid gzip data",
					})
					return
				}
				gr = v
			} else {
				var err error
				gr, err = gzip.NewReader(r.Body)
				if err != nil {
					httputil.WriteAPIError(w, httputil.APIError{
						Status: http.StatusBadRequest,
						Code:   "invalid_gzip",
						Detail: "invalid gzip data",
					})
					return
				}
			}
			defer func() {
				gr.Close()
				gzipReaderPool.Put(gr)
			}()
			r.Body = &readCloser{Reader: gr, Closer: r.Body}
			r.Header.Del("Content-Encoding")

		case "deflate":
			fr := flate.NewReader(r.Body)
			r.Body = &readCloser{Reader: fr, Closer: r.Body}
			r.Header.Del("Content-Encoding")

		case "", "identity":
			// no decompression needed

		default:
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusBadRequest,
				Code:   "unsupported_content_encoding",
				Detail: "unsupported Content-Encoding: " + encoding,
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

// readCloser wraps a Reader with an underlying Closer, closing both on Close.
type readCloser struct {
	io.Reader
	Closer io.Closer
}

func (rc *readCloser) Close() error {
	// Close the decompressor if it implements io.Closer
	if c, ok := rc.Reader.(io.Closer); ok {
		c.Close()
	}
	return rc.Closer.Close()
}

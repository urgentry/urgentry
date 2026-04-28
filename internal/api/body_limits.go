package api

import (
	"errors"
	"io"
	"mime/multipart"
	"net/http"

	"urgentry/internal/httputil"
)

const multipartEnvelopeOverhead = 1 << 20 // 1 MiB for boundaries and fields.

func readMultipartFile(w http.ResponseWriter, r *http.Request, field string, maxBytes int64) ([]byte, *multipart.FileHeader, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+multipartEnvelopeOverhead)
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		return nil, nil, normalizeBodyLimitError(err)
	}
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	file, header, err := r.FormFile(field)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	data, err := readAtMost(file, maxBytes)
	if err != nil {
		return nil, nil, err
	}
	return data, header, nil
}

func readAtMost(r io.Reader, maxBytes int64) ([]byte, error) {
	limited := &io.LimitedReader{R: r, N: maxBytes + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errRequestBodyTooLarge
	}
	return data, nil
}

func normalizeBodyLimitError(err error) error {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return errRequestBodyTooLarge
	}
	return err
}

func writeMultipartError(w http.ResponseWriter, err error, invalidMessage string) {
	if errors.Is(err, errRequestBodyTooLarge) {
		httputil.WriteError(w, http.StatusRequestEntityTooLarge, "Request body too large.")
		return
	}
	httputil.WriteError(w, http.StatusBadRequest, invalidMessage)
}

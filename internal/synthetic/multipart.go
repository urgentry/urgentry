package synthetic

import (
	"bytes"
	"mime/multipart"
	"net/textproto"
	"sort"
)

type multipartPart struct {
	FieldName   string
	FileName    string
	ContentType string
	Body        []byte
}

func BuildMultipart(parts []multipartPart, fields map[string]string) ([]byte, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.SetBoundary("urgentry-synthetic-boundary"); err != nil {
		return nil, "", err
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := fields[key]
		if err := writer.WriteField(key, value); err != nil {
			return nil, "", err
		}
	}
	for _, part := range parts {
		header := make(textproto.MIMEHeader)
		disposition := `form-data; name="` + part.FieldName + `"`
		if part.FileName != "" {
			disposition += `; filename="` + part.FileName + `"`
		}
		header.Set("Content-Disposition", disposition)
		if part.ContentType != "" {
			header.Set("Content-Type", part.ContentType)
		}
		filePart, err := writer.CreatePart(header)
		if err != nil {
			return nil, "", err
		}
		if _, err := filePart.Write(part.Body); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

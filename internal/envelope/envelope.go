package envelope

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Envelope represents a Sentry envelope: a header line followed by zero or
// more items, each consisting of an item-header line and a payload.
type Envelope struct {
	Header EnvelopeHeader
	Items  []Item
}

// EnvelopeHeader is the first JSON line in an envelope.
type EnvelopeHeader struct {
	EventID string `json:"event_id"`
	DSN     string `json:"dsn"`
	SentAt  string `json:"sent_at"`
	SDK     *SDK   `json:"sdk,omitempty"`
}

// SDK identifies the sending SDK.
type SDK struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ItemHeader is the JSON line that precedes each item payload.
type ItemHeader struct {
	Type        string `json:"type"`
	Length      int    `json:"length"`
	ContentType string `json:"content_type,omitempty"`
	Filename    string `json:"filename,omitempty"`
}

// Item is a single item inside an envelope.
type Item struct {
	Header  ItemHeader
	Payload []byte
}

// Parse parses a raw Sentry envelope from bytes.
//
// Format:
//
//	line 1: JSON envelope header
//	then repeating pairs:
//	  item header line (JSON)
//	  payload (exactly length bytes, or until next newline if length==0)
func Parse(data []byte) (*Envelope, error) {
	if len(data) == 0 {
		return nil, errors.New("envelope: empty input")
	}

	// Split the first line (envelope header).
	headerLine, rest := splitLine(data)

	// An envelope that is just whitespace/newline is valid with zero items.
	headerLine = bytes.TrimSpace(headerLine)
	if len(headerLine) == 0 {
		return &Envelope{}, nil
	}

	var header EnvelopeHeader
	if err := json.Unmarshal(headerLine, &header); err != nil {
		return nil, fmt.Errorf("envelope: invalid header JSON: %w", err)
	}

	env := &Envelope{Header: header}

	// Parse items.
	for len(rest) > 0 {
		// Trim leading newlines between items (defensive).
		rest = bytes.TrimLeft(rest, "\n")
		if len(rest) == 0 {
			break
		}

		// Read item header line.
		itemHeaderLine, afterItemHeader := splitLine(rest)
		itemHeaderLine = bytes.TrimSpace(itemHeaderLine)
		if len(itemHeaderLine) == 0 {
			break
		}

		var ih ItemHeader
		if err := json.Unmarshal(itemHeaderLine, &ih); err != nil {
			return nil, fmt.Errorf("envelope: invalid item header JSON: %w", err)
		}

		var payload []byte
		if ih.Length > 0 {
			// Length-prefixed: read exactly N bytes.
			if len(afterItemHeader) < ih.Length {
				return nil, fmt.Errorf("envelope: item %q declared length %d but only %d bytes remain",
					ih.Type, ih.Length, len(afterItemHeader))
			}
			payload = afterItemHeader[:ih.Length]
			rest = afterItemHeader[ih.Length:]
		} else {
			// Implicit length: read until next newline.
			payload, rest = splitLine(afterItemHeader)
		}

		env.Items = append(env.Items, Item{
			Header:  ih,
			Payload: payload,
		})
	}

	return env, nil
}

// splitLine splits data at the first newline. The newline is consumed but not
// included in either part. If there is no newline, the entire data is returned
// as the first part and the second part is nil.
func splitLine(data []byte) (line, rest []byte) {
	idx := bytes.IndexByte(data, '\n')
	if idx < 0 {
		return data, nil
	}
	return data[:idx], data[idx+1:]
}

package bundle

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"encoding/json/jsontext"
)

// CanonicalizeJSON returns the RFC 8785 JSON Canonicalization Scheme form of
// one JSON value. Duplicate object names and trailing JSON values are rejected.
func CanonicalizeJSON(raw []byte) ([]byte, error) {
	decoder := jsontext.NewDecoder(bytes.NewReader(raw), jsontext.AllowDuplicateNames(false), jsontext.AllowInvalidUTF8(false))
	value, err := decoder.ReadValue()
	if err != nil {
		return nil, fmt.Errorf("read JSON value: %w", err)
	}
	canonical := append(jsontext.Value(nil), value...)
	if _, err := decoder.ReadValue(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("JSON contains more than one value")
		}
		return nil, fmt.Errorf("check JSON trailer: %w", err)
	}
	if err := canonical.Canonicalize(); err != nil {
		return nil, fmt.Errorf("format RFC 8785 JSON: %w", err)
	}
	return []byte(canonical), nil
}

package input

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// DecodeJSON decodes exactly one JSON value from a request body.
func DecodeJSON[T any](r *http.Request) (T, error) {
	var value T
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return value, fmt.Errorf("request body must contain exactly one JSON value")
		}
		return value, err
	}

	return value, nil
}

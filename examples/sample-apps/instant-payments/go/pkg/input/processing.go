package input

import (
	"encoding/json"
	"net/http"
)

func DecodeJSON[T any](r *http.Request) (T, error) {
	var value T
	err := json.NewDecoder(r.Body).Decode(&value)

	return value, err
}

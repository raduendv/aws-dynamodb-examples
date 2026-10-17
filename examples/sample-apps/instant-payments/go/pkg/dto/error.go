package dto

// Error is the common error payload returned by the API.
type Error struct {
	Error   error  `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}

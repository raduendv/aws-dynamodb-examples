package dto

// Error is a legacy error DTO, not the business API error envelope.
// Business handlers use the internal response package's WriteError helper.
type Error struct {
	Error   error  `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}

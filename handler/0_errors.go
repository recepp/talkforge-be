package handler

// ErrorResponse represents an error payload returned by the API.
type ErrorResponse struct {
	Error string `json:"error" example:"invalid request parameters"`
}

package relayclient

// PublicCode exposes only the transport's bounded stable failure category.
func (e *Error) PublicCode() string { return e.Code }

// PublicStatus preserves HTTP classification without exposing response details.
func (e *Error) PublicStatus() int { return e.Status }

package chessdto

type DomainError struct {
	Code      string
	Message   string
	Retryable bool
}

func (e DomainError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	return "chess service error"
}

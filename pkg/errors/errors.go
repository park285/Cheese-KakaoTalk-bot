package errors

import "fmt"

const (
	CodeBotError    = "BOT_ERROR"
	CodeAPIError    = "API_ERROR"
	CodeValidation  = "VALIDATION_ERROR"
	CodeCache       = "CACHE_ERROR"
	CodeService     = "SERVICE_ERROR"
	CodeKeyRotation = "KEY_ROTATION_ERROR"
)

type BotError struct {
	Message    string
	Code       string
	StatusCode int
	Context    map[string]any
	Cause      error
}

func (e *BotError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *BotError) Unwrap() error {
	return e.Cause
}

func NewBotError(message, code string, statusCode int, context map[string]any) *BotError {
	return &BotError{
		Message:    message,
		Code:       code,
		StatusCode: statusCode,
		Context:    context,
	}
}

func (e *BotError) WithCause(cause error) *BotError {
	e.Cause = cause
	return e
}

type APIError struct {
	*BotError
}

func NewAPIError(message string, statusCode int, context map[string]any) *APIError {
	return &APIError{
		BotError: &BotError{
			Message:    message,
			Code:       CodeAPIError,
			StatusCode: statusCode,
			Context:    context,
		},
	}
}

type ValidationError struct {
	*BotError
	Field string
	Value interface{}
}

func NewValidationError(message, field string, value interface{}) *ValidationError {
	return &ValidationError{
		BotError: &BotError{
			Message:    message,
			Code:       CodeValidation,
			StatusCode: 400,
			Context: map[string]any{
				"field": field,
				"value": value,
			},
		},
		Field: field,
		Value: value,
	}
}

type CacheError struct {
	*BotError
	Operation string
	Key       string
}

func NewCacheError(message, operation, key string, cause error) *CacheError {
	return &CacheError{
		BotError: &BotError{
			Message:    message,
			Code:       CodeCache,
			StatusCode: 500,
			Context: map[string]any{
				"operation": operation,
				"key":       key,
			},
			Cause: cause,
		},
		Operation: operation,
		Key:       key,
	}
}

type ServiceError struct {
	*BotError
	Service   string
	Operation string
}

func NewServiceError(message, service, operation string, cause error) *ServiceError {
	return &ServiceError{
		BotError: &BotError{
			Message:    message,
			Code:       CodeService,
			StatusCode: 500,
			Context: map[string]any{
				"service":   service,
				"operation": operation,
			},
			Cause: cause,
		},
		Service:   service,
		Operation: operation,
	}
}

type KeyRotationError struct {
	*APIError
}

func NewKeyRotationError(message string, statusCode int, context map[string]any) *KeyRotationError {
	return &KeyRotationError{
		APIError: &APIError{
			BotError: &BotError{
				Message:    message,
				Code:       CodeKeyRotation,
				StatusCode: statusCode,
				Context:    context,
			},
		},
	}
}

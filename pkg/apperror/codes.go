package apperror

type AppErrorKind struct {
	Code    string // App-specific code (used by frontend)
	Message string // Human-readable message
}

// Predefined, reusable error kinds

var (
	// --- Authentication ---
	ErrInvalidCredentials = AppErrorKind{"INVALID_CREDENTIALS", "Invalid email or password"}
	ErrUnauthorized       = AppErrorKind{"UNAUTHORIZED", "You are not authorized"}
	ErrForbidden          = AppErrorKind{"FORBIDDEN", "You do not have access"}
	ErrInvalidHeaders     = AppErrorKind{"INVALID_HEADERS", "Client provide invalid headers"}

	// --- User ---
	ErrUserExists   = AppErrorKind{"USER_ALREADY_EXISTS", "User already exists"}
	ErrUserNotFound = AppErrorKind{"USER_NOT_FOUND", "User not found"}

	// --- Validation ---
	ErrValidationFailed   = AppErrorKind{"VALIDATION_ERROR", "Validation failed"}
	ErrInvalidRequestBody = AppErrorKind{"INVALID_REQUEST_BODY", "Invalid request body"}

	// --- Payments ---
	ErrPaymentFailed = AppErrorKind{"PAYMENT_FAILED", "Payment processing failed"}

	// --- Rate Limits ---
	ErrRateLimitExceeded = AppErrorKind{"RATE_LIMIT_EXCEEDED", "Too many requests"}

	// --- System ---
	ErrInternal = AppErrorKind{"INTERNAL_ERROR", "An internal error occurred"}
)

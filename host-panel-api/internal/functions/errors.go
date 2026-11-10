package functions

import (
	"errors"
	"fmt"
)

// Error codes returned to clients.
const (
	CodeInvalidName      = "INVALID_FUNCTION_NAME"
	CodeInvalidRuntime   = "INVALID_FUNCTION_RUNTIME"
	CodeInvalidHandler   = "INVALID_FUNCTION_HANDLER"
	CodeInvalidEnv       = "INVALID_FUNCTION_ENV"
	CodeReservedEnv      = "FUNCTION_ENV_RESERVED"
	CodeInvalidSecret    = "INVALID_FUNCTION_SECRET"
	CodeInvalidLimits    = "INVALID_FUNCTION_LIMITS"
	CodeInvalidNetwork   = "INVALID_FUNCTION_NETWORK"
	CodeInvalidCode      = "FUNCTION_CODE_INVALID"
	CodeCodeTooLarge     = "FUNCTION_CODE_TOO_LARGE"
	CodeCodeTimeout      = "FUNCTION_CODE_TIMEOUT"
	CodeBusy             = "FUNCTION_UPLOADS_BUSY"
	CodeNoSpace          = "FUNCTION_NO_SPACE"
	CodeRuntimeUntrusted = "FUNCTION_RUNTIME_UNTRUSTED"
	// No trusted runtime binary is installed yet (download in progress or
	// failed, details in the logs): retry later.
	CodeRuntimeUnavailable = "FUNCTION_RUNTIME_UNAVAILABLE"
	CodeArtifactModified   = "FUNCTION_ARTIFACT_MODIFIED"
	CodeInvalidReference   = "FUNCTION_REFERENCE_INVALID"
	CodeStorage            = "FUNCTION_STORAGE_ERROR"
	CodeInvalidDeployment  = "INVALID_FUNCTION_DEPLOYMENT"
	// Custom runtime (provided): the bootstrap of the code.
	CodeBootstrapMissing = "FUNCTION_BOOTSTRAP_MISSING"
	CodeBootstrapArch    = "FUNCTION_BOOTSTRAP_ARCH"
	CodeBootstrapInvalid = "FUNCTION_BOOTSTRAP_INVALID"

	// Manager and HTTP API (part 2).
	CodeDisabled           = "FUNCTIONS_DISABLED"
	CodeNotFound           = "FUNCTION_NOT_FOUND"
	CodeExists             = "FUNCTION_EXISTS"
	CodeNameConflict       = "FUNCTION_NAME_CONFLICT"
	CodeLimit              = "FUNCTION_LIMIT_REACHED"
	CodeBudget             = "FUNCTION_BUDGET_EXCEEDED"
	CodeInProgress         = "FUNCTION_OPERATION_IN_PROGRESS"
	CodeModified           = "FUNCTION_MODIFIED"
	CodeNotModified        = "FUNCTION_NOT_MODIFIED"
	CodeNotDeployed        = "FUNCTION_NOT_DEPLOYED"
	CodeNotRunning         = "FUNCTION_NOT_RUNNING"
	CodeVerificationFailed = "FUNCTION_VERIFICATION_FAILED"
	CodeStartFailed        = "FUNCTION_START_FAILED"
	CodeOutcomeUnknown     = "FUNCTION_OUTCOME_UNKNOWN"
	CodeDeploymentNotFound = "FUNCTION_DEPLOYMENT_NOT_FOUND"
	CodeDeploymentActive   = "FUNCTION_DEPLOYMENT_ACTIVE"
	CodeSecretNotFound     = "FUNCTION_SECRET_NOT_FOUND"
	CodeImagePullFailed    = "FUNCTION_IMAGE_PULL_FAILED"
	CodeInvokeBusy         = "FUNCTION_INVOKE_BUSY"
	CodeInvokeTimeout      = "FUNCTION_INVOKE_TIMEOUT"
	CodeInvokeFailed       = "FUNCTION_INVOKE_FAILED"
	CodeEventTooLarge      = "FUNCTION_EVENT_TOO_LARGE"
	CodeDockerError        = "FUNCTION_DOCKER_ERROR"
	CodeInvalidAction      = "INVALID_FUNCTION_ACTION"
	CodeInvalidEvent       = "INVALID_FUNCTION_EVENT"
	CodeEventTimeout       = "FUNCTION_EVENT_TIMEOUT"
)

// AllCodes lists every client-facing code (the HTTP layer must map them).
var AllCodes = []string{
	CodeInvalidName, CodeInvalidRuntime, CodeInvalidHandler, CodeInvalidEnv, CodeReservedEnv,
	CodeInvalidSecret, CodeInvalidLimits, CodeInvalidNetwork, CodeInvalidCode, CodeCodeTooLarge,
	CodeCodeTimeout, CodeBusy, CodeNoSpace, CodeRuntimeUntrusted, CodeRuntimeUnavailable, CodeArtifactModified,
	CodeInvalidReference, CodeStorage, CodeInvalidDeployment,
	CodeBootstrapMissing, CodeBootstrapArch, CodeBootstrapInvalid,
	CodeDisabled, CodeNotFound, CodeExists, CodeNameConflict, CodeLimit, CodeBudget, CodeInProgress,
	CodeModified, CodeNotModified, CodeNotDeployed, CodeNotRunning, CodeVerificationFailed,
	CodeStartFailed, CodeOutcomeUnknown, CodeDeploymentNotFound, CodeDeploymentActive,
	CodeSecretNotFound, CodeImagePullFailed, CodeInvokeBusy, CodeInvokeTimeout, CodeInvokeFailed,
	CodeEventTooLarge, CodeDockerError, CodeInvalidAction, CodeInvalidEvent, CodeEventTimeout,
}

// Codes whose message may hold host paths or daemon errors: the HTTP layer
// replaces it with a generic one (the details are logged).
var InternalCodes = map[string]bool{
	CodeStorage: true, CodeInvalidReference: true, CodeArtifactModified: true, CodeDockerError: true,
}

// Error is a typed, client-facing error. Messages never contain a secret
// value; names coming from a zip are quoted and truncated.
type Error struct {
	Code    string
	Field   string
	Message string
}

func (e *Error) Error() string { return e.Message }

func newError(code, field, format string, args ...any) *Error {
	return &Error{Code: code, Field: field, Message: fmt.Sprintf(format, args...)}
}

// CodeOf returns the code of a functions error ("" otherwise).
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// quoteName quotes an untrusted name for an error message, bounded so that
// a crafted zip cannot flood the logs or the response.
func quoteName(s string) string {
	const max = 120
	if len(s) > max {
		s = s[:max] + "…"
	}
	return fmt.Sprintf("%q", s)
}

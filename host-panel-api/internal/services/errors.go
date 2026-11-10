package services

import (
	"errors"
	"fmt"
)

// Error codes returned to clients.
const (
	CodeDisabled              = "SERVICES_DISABLED"
	CodeInvalidEngine         = "INVALID_SERVICE_ENGINE"
	CodeInvalidVersion        = "INVALID_SERVICE_VERSION"
	CodeInvalidName           = "INVALID_RESOURCE_NAME"
	CodeNameReserved          = "RESOURCE_NAME_RESERVED"
	CodeInstanceNotFound      = "SERVICE_INSTANCE_NOT_FOUND"
	CodeResourceNotFound      = "RESOURCE_NOT_FOUND"
	CodeResourceExists        = "RESOURCE_EXISTS"
	CodeResourceNotManaged    = "RESOURCE_NOT_MANAGED"
	CodeResourceLimit         = "RESOURCE_LIMIT_REACHED"
	CodeInProgress            = "SERVICE_OPERATION_IN_PROGRESS"
	CodeDirectoryConflict     = "SERVICE_DIRECTORY_CONFLICT"
	CodeDataDirNotEmpty       = "SERVICE_DATA_DIR_NOT_EMPTY"
	CodeInstanceModified      = "SERVICE_INSTANCE_MODIFIED"
	CodeInstanceNotEmpty      = "SERVICE_INSTANCE_NOT_EMPTY"
	CodeNotReady              = "SERVICE_INSTANCE_NOT_READY"
	CodeStartFailed           = "SERVICE_INSTANCE_START_FAILED"
	CodeEngineError           = "SERVICE_ENGINE_ERROR"
	CodeOutcomeUnknown        = "SERVICE_OUTCOME_UNKNOWN"
	CodeStorage               = "SERVICES_STORAGE_ERROR"
	CodeInvalidOption         = "INVALID_SERVICE_OPTION"
	CodeInstanceLimit         = "SERVICE_INSTANCE_LIMIT_REACHED"
	CodeOptionImmutable       = "SERVICE_OPTION_IMMUTABLE"
	CodePasswordResetRequired = "SERVICE_PASSWORD_RESET_REQUIRED"
	CodeInstanceNotModified   = "SERVICE_INSTANCE_NOT_MODIFIED"
	CodeNoCredentials         = "SERVICE_NO_CREDENTIALS"
	CodeInvalidNetwork        = "INVALID_SERVICE_NETWORK"
	CodeLastNetwork           = "SERVICE_LAST_NETWORK"
	CodeInvalidExposure       = "INVALID_SERVICE_EXPOSURE"
	CodePortInUse             = "SERVICE_PORT_IN_USE"
	CodeExposureFailed        = "SERVICE_EXPOSURE_FAILED"
	CodeUpdateNotSupported    = "SERVICE_UPDATE_NOT_SUPPORTED"
	CodeVersionDowngrade      = "SERVICE_VERSION_DOWNGRADE"
	CodePullFailed            = "SERVICE_IMAGE_PULL_FAILED"
	CodeReleasesUnavailable   = "SERVICE_RELEASES_UNAVAILABLE"
)

// AllCodes lists every client-facing code (tests check they are mapped).
var AllCodes = []string{
	CodeDisabled, CodeInvalidEngine, CodeInvalidVersion, CodeInvalidName, CodeNameReserved,
	CodeInstanceNotFound, CodeResourceNotFound, CodeResourceExists, CodeResourceNotManaged,
	CodeResourceLimit, CodeInProgress, CodeDirectoryConflict, CodeDataDirNotEmpty,
	CodeInstanceModified, CodeInstanceNotEmpty, CodeNotReady, CodeStartFailed,
	CodeEngineError, CodeOutcomeUnknown, CodeStorage, CodeInvalidOption, CodeInstanceLimit, CodeOptionImmutable, CodePasswordResetRequired, CodeInstanceNotModified, CodeNoCredentials,
	CodeInvalidNetwork, CodeLastNetwork, CodeInvalidExposure, CodePortInUse, CodeExposureFailed,
	CodeUpdateNotSupported, CodeVersionDowngrade, CodePullFailed, CodeReleasesUnavailable,
}

// Error is a typed, client-facing error. Message is an English fallback that
// never contains engine output (which may echo secrets).
type Error struct {
	Code    string
	Field   string
	Message string
}

func (e *Error) Error() string { return e.Message }

func newError(code, field, format string, args ...any) *Error {
	return &Error{Code: code, Field: field, Message: fmt.Sprintf(format, args...)}
}

// CodeOf returns the code of a services error, or CodeStorage.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeStorage
}

// Sentinel errors returned by provisioners; the Manager maps them to codes.
var (
	ErrResourceExists   = errors.New("resource already exists")
	ErrResourceNotFound = errors.New("resource not found")
	// ErrOutcomeUnknown: the command timed out or did not report its end;
	// it may still have completed (Docker does not kill an exec).
	ErrOutcomeUnknown = errors.New("outcome unknown")
)

// EngineError is a failure reported by the engine. Detail is safe to log (an
// SQLSTATE or an error code, never the engine output).
type EngineError struct {
	Op     string
	Detail string
}

func (e *EngineError) Error() string { return e.Op + ": engine error " + e.Detail }

// errModifiedByHand is returned when an operation needs files the panel
// would execute, but they differ from what it renders.
func errModifiedByHand() *Error {
	return newError(CodeInstanceModified, "", "the instance files were edited by hand: restore them or remove the instance manually")
}

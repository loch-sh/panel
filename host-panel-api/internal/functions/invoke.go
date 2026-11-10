package functions

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"
)

// Invocations go through `docker exec <fn> /loch/lambda-runtime invoke
// --log-tail` (event on stdin), never through a network bridge (decision
// log §20, plan reviews). The flow itself comes in a later part; the
// interface and the parsing of the runtime's envelope are defined here.

// Bounds of an invocation.
const (
	MaxEventBytes = 6 << 20
	// maxPayloadBytes: the runtime's response limit (6 MiB + 100) plus the
	// allowance of the invoke subcommand for the API errors (64 KiB).
	maxPayloadBytes = 6<<20 + 100 + 64<<10
	maxLogTailBytes = 4096
	// maxEnvelopeBytes bounds the runtime's stdout: base64 of the payload
	// and of the log tail, plus the JSON around them.
	maxEnvelopeBytes = (maxPayloadBytes+maxLogTailBytes)*4/3 + 4096
)

// InvokeResult is the outcome of an invocation, as reported by the runtime.
// It is untrusted data (written by the function container): rendered as
// text only, never with the function's headers or content type.
type InvokeResult struct {
	StatusCode    int    `json:"statusCode"`
	FunctionError string `json:"functionError,omitempty"` // "Unhandled" or empty
	RequestID     string `json:"requestId"`
	DurationMs    int64  `json:"durationMs"`
	Payload       []byte `json:"payload"`
	LogTail       []byte `json:"logTail"`
}

// Invoker invokes a verified function container.
type Invoker interface {
	Invoke(ctx context.Context, containerID string, event []byte, timeout time.Duration) (InvokeResult, error)
}

var (
	requestIDRe     = regexp.MustCompile(`\A[0-9a-fA-F-]{1,64}\z`)
	functionErrorRe = regexp.MustCompile(`\A[A-Za-z]{0,32}\z`)
)

// ParseEnvelope decodes the JSON envelope the runtime writes on stdout
// ({"statusCode","functionError","requestId","durationMs","payload":base64,
// "logTail":base64}), strictly and within bounds.
func ParseEnvelope(stdout []byte) (InvokeResult, error) {
	if len(stdout) > maxEnvelopeBytes {
		return InvokeResult{}, errors.New("invoke envelope too large")
	}
	var raw struct {
		StatusCode    int    `json:"statusCode"`
		FunctionError string `json:"functionError"`
		RequestID     string `json:"requestId"`
		DurationMs    int64  `json:"durationMs"`
		Payload       string `json:"payload"`
		LogTail       string `json:"logTail"`
	}
	dec := json.NewDecoder(bytes.NewReader(stdout))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return InvokeResult{}, errors.New("invalid invoke envelope")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return InvokeResult{}, errors.New("invalid invoke envelope: trailing data")
	}
	if raw.StatusCode < 100 || raw.StatusCode > 599 || raw.DurationMs < 0 ||
		!functionErrorRe.MatchString(raw.FunctionError) ||
		(raw.RequestID != "" && !requestIDRe.MatchString(raw.RequestID)) {
		return InvokeResult{}, errors.New("invalid invoke envelope fields")
	}
	payload, err := base64.StdEncoding.DecodeString(raw.Payload)
	if err != nil || len(payload) > maxPayloadBytes {
		return InvokeResult{}, errors.New("invalid invoke payload")
	}
	logTail, err := base64.StdEncoding.DecodeString(raw.LogTail)
	if err != nil || len(logTail) > maxLogTailBytes {
		return InvokeResult{}, errors.New("invalid invoke log tail")
	}
	return InvokeResult{
		StatusCode: raw.StatusCode, FunctionError: raw.FunctionError, RequestID: raw.RequestID,
		DurationMs: raw.DurationMs, Payload: payload, LogTail: logTail,
	}, nil
}

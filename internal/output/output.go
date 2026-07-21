package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	ExitOK          = 0
	ExitUnexpected  = 1
	ExitValidation  = 2
	ExitAuth        = 3
	ExitRemote      = 4
	ExitUnsupported = 5
)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  any    `json:"detail,omitempty"`
}

type Envelope struct {
	OK        bool     `json:"ok"`
	Service   string   `json:"service"`
	Operation string   `json:"operation"`
	Data      any      `json:"data,omitempty"`
	Error     *Error   `json:"error,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

type ExitError struct {
	ExitCode int
	Err      Error
}

func (e *ExitError) Error() string {
	return e.Err.Message
}

func NewError(exitCode int, code, message string, detail any) *ExitError {
	return &ExitError{ExitCode: exitCode, Err: Error{Code: code, Message: message, Detail: detail}}
}

func Validation(code, message string, detail any) *ExitError {
	return NewError(ExitValidation, code, message, detail)
}

func Auth(code, message string, detail any) *ExitError {
	return NewError(ExitAuth, code, message, detail)
}

func Remote(code, message string, detail any) *ExitError {
	return NewError(ExitRemote, code, message, detail)
}

func Unsupported(service, message string) *ExitError {
	return NewError(ExitUnsupported, "unsupported_service", message, map[string]string{"service": service})
}

func Write(w io.Writer, env Envelope) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(env)
}

func Success(w io.Writer, service, operation string, data any, warnings ...string) int {
	_ = Write(w, Envelope{OK: true, Service: service, Operation: operation, Data: data, Warnings: cleanWarnings(warnings)})
	return ExitOK
}

func Failure(w io.Writer, service, operation string, err error) int {
	exitCode := ExitUnexpected
	outErr := Error{Code: "unexpected_error", Message: err.Error()}
	var e *ExitError
	if errors.As(err, &e) {
		exitCode = e.ExitCode
		outErr = e.Err
	}
	_ = Write(w, Envelope{OK: false, Service: service, Operation: operation, Error: &outErr})
	return exitCode
}

func cleanWarnings(warnings []string) []string {
	out := warnings[:0]
	for _, warning := range warnings {
		if warning != "" {
			out = append(out, warning)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func RedactSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "****"
	}
	return fmt.Sprintf("%s****%s", s[:2], s[len(s)-2:])
}

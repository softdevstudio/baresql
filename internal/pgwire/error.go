package pgwire

import (
	"errors"
	"fmt"
	"strconv"
)

// Error is an ErrorResponse or NoticeResponse from the server.
type Error struct {
	Severity string // ERROR, FATAL, PANIC, WARNING, NOTICE, ...
	Code     string // SQLSTATE, e.g. 42501 insufficient_privilege
	Message  string
	Detail   string
	Hint     string
	Position int // 1-based character position in the query, 0 when absent
	Where    string
	Schema   string
	Table    string
	Column   string
	DataType string
	// Constraint is the name of the constraint that failed, if any.
	Constraint string
	Routine    string
}

func (e *Error) Error() string {
	sev := e.Severity
	if sev == "" {
		sev = "ERROR"
	}
	return fmt.Sprintf("%s: %s (SQLSTATE %s)", sev, e.Message, e.Code)
}

// parseError reads the fields of an ErrorResponse or NoticeResponse. Field
// types it doesn't know are skipped, as the protocol asks.
func parseError(body []byte) *Error {
	e := &Error{}
	r := &reader{b: body}
	for r.err == nil {
		t := r.byte()
		if t == 0 || r.err != nil {
			break
		}
		v := r.cstr()
		switch t {
		case 'V': // severity, never localized (9.6 and later)
			e.Severity = v
		case 'S':
			if e.Severity == "" {
				e.Severity = v
			}
		case 'C':
			e.Code = v
		case 'M':
			e.Message = v
		case 'D':
			e.Detail = v
		case 'H':
			e.Hint = v
		case 'P':
			e.Position, _ = strconv.Atoi(v)
		case 'W':
			e.Where = v
		case 's':
			e.Schema = v
		case 't':
			e.Table = v
		case 'c':
			e.Column = v
		case 'd':
			e.DataType = v
		case 'n':
			e.Constraint = v
		case 'R':
			e.Routine = v
		}
	}
	if e.Message == "" {
		e.Message = "(no message)"
	}
	return e
}

// IsCode reports whether err is (or wraps) a server error with the given
// SQLSTATE.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

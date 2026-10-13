// Package result owns the ordinary-command JSON and exit-code contract.
package result

type Result struct {
	SchemaVersion int      `json:"schema_version"`
	OK            bool     `json:"ok"`
	Code          string   `json:"code"`
	Operation     string   `json:"operation"`
	Project       string   `json:"project,omitempty"`
	Scope         string   `json:"scope,omitempty"`
	Copies        []string `json:"copies,omitempty"`
	Summary       string   `json:"summary"`
	Items         any      `json:"items"`
	Recovery      any      `json:"recovery,omitempty"`
	Warnings      []string `json:"warnings"`
}

func New(operation string, items any) Result {
	return Result{SchemaVersion: 1, OK: true, Code: "OK", Operation: operation, Items: items, Warnings: []string{}}
}

type Error struct {
	Code     string
	Message  string
	Exit     int
	Recovery any
}

func (e *Error) Error() string { return e.Message }

func Fail(code, message string, exit int) *Error {
	return &Error{Code: code, Message: message, Exit: exit}
}

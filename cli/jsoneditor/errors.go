package jsoneditor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrAborted is returned by Run (and the node-edit form) when the user
// leaves the editor with Esc or Ctrl-C instead of saving.
var ErrAborted = errors.New("you closed the editor without saving, so nothing was changed. " +
	"To keep your edits next time, press Ctrl-D to save before you leave")

// RunFailed explains the editor program itself failing, as opposed to the
// user leaving it.
func RunFailed(err error) error {
	return fmt.Errorf("the editor stopped unexpectedly (%w), so nothing was saved. "+
		"If your terminal can't show full-screen programs, pass the updated JSON with --file instead", err)
}

// DescribeJSONError rewrites a json.Unmarshal or Decoder error about data
// into a message that says where the problem is and what was expected:
// the line and column of a syntax error, or the field and the type it
// needed. The original error stays wrapped for errors.As. Other errors,
// and nil, pass through unchanged.
func DescribeJSONError(data []byte, err error) error {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		return syntaxError(data, syn)
	case errors.As(err, &typ):
		got := describeJSONValue(typ.Value)
		want := describeGoType(typ.Type)
		if typ.Field == "" {
			return fmt.Errorf("the document should be %s, not %s (%w)", want, got, err)
		}
		return fmt.Errorf("%q should be %s, not %s (%w)", typ.Field, want, got, err)
	case errors.Is(err, io.ErrUnexpectedEOF):
		return truncatedError(err)
	}
	return err
}

// syntaxError places a *json.SyntaxError at a line and column of data, and
// adds a hint when the likely cause is text missing its quotes.
func syntaxError(data []byte, syn *json.SyntaxError) error {
	if syn.Error() == "unexpected end of JSON input" {
		return truncatedError(syn)
	}
	at := int(syn.Offset) - 1 // Offset counts the byte that failed
	line, col := lineCol(data, at)
	msg := fmt.Sprintf("line %d, column %d: %s", line, col, syn.Error())
	if strings.Contains(syn.Error(), "looking for beginning of value") && at >= 0 && at < len(data) {
		if r, _ := utf8.DecodeRune(data[at:]); unicode.IsLetter(r) {
			word := data[at:]
			if end := bytes.IndexFunc(word, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.' }); end >= 0 {
				word = word[:end]
			}
			msg += fmt.Sprintf(". Text values go in double quotes, like %q", word)
		}
	}
	return &locatedError{msg: msg, err: syn}
}

// truncatedError explains a document that stops partway through a value.
func truncatedError(err error) error {
	return &locatedError{
		msg: "the JSON ends too early. Check that every { has a matching } and every [ has a matching ]",
		err: err,
	}
}

// locatedError carries a rewritten message while keeping the original
// error reachable through errors.Is and errors.As.
type locatedError struct {
	msg string
	err error
}

func (e *locatedError) Error() string { return e.msg }
func (e *locatedError) Unwrap() error { return e.err }

// lineCol returns the 1-based line and column (in characters) of byte
// offset at in data. An offset past the end lands just after the last
// character.
func lineCol(data []byte, at int) (int, int) {
	if at < 0 {
		at = 0
	}
	if at > len(data) {
		at = len(data)
	}
	before := data[:at]
	line := bytes.Count(before, []byte("\n")) + 1
	start := bytes.LastIndexByte(before, '\n') + 1
	return line, utf8.RuneCount(before[start:]) + 1
}

// describeJSONValue turns json.UnmarshalTypeError's Value ("string",
// "array", "number 1.5", ...) into words.
func describeJSONValue(v string) string {
	switch {
	case v == "string":
		return "text"
	case v == "array":
		return "a list"
	case v == "object":
		return "an object"
	case v == "bool":
		return "true or false"
	case v == "number":
		return "a number"
	case strings.HasPrefix(v, "number "):
		return "the number " + strings.TrimPrefix(v, "number ")
	}
	return v
}

// describeGoType says, in JSON terms, what a field of type t has to hold.
func describeGoType(t reflect.Type) string {
	if t == nil {
		return "a different kind of value"
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		return `a list, like ["a", "b"]`
	case reflect.Map, reflect.Struct:
		return `an object, like {"key": "value"}`
	case reflect.String:
		return `text in double quotes, like "value"`
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "a whole number"
	case reflect.Float32, reflect.Float64:
		return "a number"
	}
	return "a different kind of value"
}

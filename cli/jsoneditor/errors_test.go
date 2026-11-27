package jsoneditor

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func wantMsg(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error mentioning %q", wants)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got:\n%s", want, err)
		}
	}
}

func TestParseTreeSaysWhereTheSyntaxErrorIs(t *testing.T) {
	_, err := parseTree([]byte("{\n  \"a\": 1,\n  \"b\": }\n"))
	wantMsg(t, err, "line 3, column 8")
	var syn *json.SyntaxError
	if !errors.As(err, &syn) {
		t.Errorf("error should still wrap *json.SyntaxError: %v", err)
	}
}

func TestParseTreeSuggestsQuotesForBareText(t *testing.T) {
	_, err := parseTree([]byte(`web01`))
	wantMsg(t, err, "line 1, column 1", "double quotes", `"web01"`)
}

func TestParseTreeExplainsTrailingData(t *testing.T) {
	_, err := parseTree([]byte("{}\n{}"))
	wantMsg(t, err, "line 2, column 1", "more text after", "one value")
}

func TestParseTreeExplainsATruncatedDocument(t *testing.T) {
	_, err := parseTree([]byte(`{"a": [1, 2`))
	wantMsg(t, err, "ends too early", "}", "]")
	_, err = parseTree([]byte("  "))
	wantMsg(t, err, "empty")
}

func TestDescribeJSONErrorNamesTheFieldAndTypes(t *testing.T) {
	var v struct {
		RunList []string `json:"run_list"`
	}
	data := []byte(`{"run_list": "recipe[nginx]"}`)
	err := DescribeJSONError(data, json.Unmarshal(data, &v))
	wantMsg(t, err, `"run_list"`, "should be a list", "not text")
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Errorf("error should still wrap *json.UnmarshalTypeError: %v", err)
	}
}

func TestDescribeJSONErrorLocatesSyntaxErrors(t *testing.T) {
	data := []byte("{\n  \"a\": 1,\n}")
	var v map[string]any
	err := DescribeJSONError(data, json.Unmarshal(data, &v))
	wantMsg(t, err, "line 3, column 1")
}

func TestDescribeJSONErrorPassesOthersThrough(t *testing.T) {
	other := errors.New("boom")
	if got := DescribeJSONError(nil, other); got != other {
		t.Errorf("DescribeJSONError(other) = %v, want it unchanged", got)
	}
	if DescribeJSONError(nil, nil) != nil {
		t.Error("DescribeJSONError(nil) should be nil")
	}
}

func TestErrAbortedSaysNothingWasSaved(t *testing.T) {
	wantMsg(t, ErrAborted, "without saving", "nothing", "Ctrl-D")
}

func TestRunFailureSaysNothingWasSaved(t *testing.T) {
	err := RunFailed(errors.New("could not open a new TTY"))
	wantMsg(t, err, "editor", "could not open a new TTY", "nothing was saved", "--file")
}

// A save that fails to parse in raw mode tells the user it wasn't saved.
func TestRawSaveErrorSaysNotSaved(t *testing.T) {
	m := New([]byte(`{`), func([]byte) error { return nil })
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if m.Finished() {
		t.Fatal("a malformed document must not finish the edit")
	}
	if v := m.View(); !strings.Contains(v, "not saved") {
		t.Errorf("view should say the edit wasn't saved, got %q", v)
	}
}

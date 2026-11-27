package printer

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestParseFormat(t *testing.T) {
	cases := []struct {
		in   string
		want Format
		ok   bool
	}{
		{"human", FormatHuman, true},
		{"json", FormatJSON, true},
		{"xml", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, err := ParseFormat(tc.in)
		if tc.ok {
			if err != nil || got != tc.want {
				t.Errorf("ParseFormat(%q) = %q, %v; want %q, nil", tc.in, got, err, tc.want)
			}
			continue
		}
		if err == nil {
			t.Errorf("ParseFormat(%q) should have returned an error", tc.in)
			continue
		}
		if !strings.Contains(err.Error(), "human or json") || strings.Contains(err.Error(), "printer:") {
			t.Errorf("ParseFormat(%q) error = %q, want a plain message listing human and json", tc.in, err)
		}
	}
}

func TestPrinterListHumanWritesOnePerLine(t *testing.T) {
	var buf bytes.Buffer
	if err := New(&buf, FormatHuman).List([]string{"alpha", "beta", "gamma"}); err != nil {
		t.Fatalf("List: %v", err)
	}

	want := "alpha\nbeta\ngamma\n"
	if buf.String() != want {
		t.Errorf("human List = %q, want %q", buf.String(), want)
	}
}

func TestPrinterListJSONWritesArray(t *testing.T) {
	var buf bytes.Buffer
	if err := New(&buf, FormatJSON).List([]string{"alpha", "beta"}); err != nil {
		t.Fatalf("List: %v", err)
	}

	var got []string
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Errorf("json List = %v, want [alpha beta]", got)
	}
}

func TestPrinterListJSONEmptyIsArrayNotNull(t *testing.T) {
	var buf bytes.Buffer
	if err := New(&buf, FormatJSON).List(nil); err != nil {
		t.Fatalf("List: %v", err)
	}

	if got := bytes.TrimSpace(buf.Bytes()); string(got) != "[]" {
		t.Errorf("empty json List = %q, want %q", got, "[]")
	}
}

func TestPrinterValueHumanWritesPrettyJSON(t *testing.T) {
	type sample struct {
		Name    string   `json:"name"`
		RunList []string `json:"run_list"`
	}
	var buf bytes.Buffer
	if err := New(&buf, FormatHuman).Value(sample{Name: "web", RunList: []string{"recipe[apache]"}}); err != nil {
		t.Fatalf("Value (human): %v", err)
	}

	want := "{\n  \"name\": \"web\",\n  \"run_list\": [\n    \"recipe[apache]\"\n  ]\n}\n"
	if buf.String() != want {
		t.Errorf("human Value =\n%q\nwant\n%q", buf.String(), want)
	}
}

func TestPrinterValueJSONWritesPrettyJSON(t *testing.T) {
	type sample struct {
		Name string `json:"name"`
	}
	var buf bytes.Buffer
	if err := New(&buf, FormatJSON).Value(sample{Name: "web"}); err != nil {
		t.Fatalf("Value (json): %v", err)
	}
	if got := buf.String(); got != "{\n  \"name\": \"web\"\n}\n" {
		t.Errorf("json Value = %q", got)
	}
}

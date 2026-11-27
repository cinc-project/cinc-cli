// Package printer renders cinc command output in a selectable format.
package printer

import (
	"encoding/json"
	"fmt"
	"io"
)

// Format identifies an output rendering.
type Format string

const (
	// FormatHuman is human-readable plain text.
	FormatHuman Format = "human"
	// FormatJSON is machine-readable JSON.
	FormatJSON Format = "json"
)

// ParseFormat converts a format name into a Format, rejecting unknown names.
func ParseFormat(s string) (Format, error) {
	switch Format(s) {
	case FormatHuman, FormatJSON:
		return Format(s), nil
	default:
		return "", fmt.Errorf("%q isn't an output format we know. Use human or json", s)
	}
}

// Printer renders command results to a writer in a fixed format.
type Printer struct {
	w      io.Writer
	format Format
}

// New returns a Printer that writes to w in the given format.
func New(w io.Writer, format Format) *Printer {
	return &Printer{w: w, format: format}
}

// List renders a flat list of string items.
func (p *Printer) List(items []string) error {
	if p.format == FormatJSON {
		if items == nil {
			items = []string{}
		}
		enc := json.NewEncoder(p.w)
		enc.SetIndent("", "  ")
		return enc.Encode(items)
	}
	for _, item := range items {
		if _, err := fmt.Fprintln(p.w, item); err != nil {
			return err
		}
	}
	return nil
}

// Value renders an arbitrary structured command result. For server-object
// payloads (roles, environments, clients, …) the human format is pretty-
// printed JSON: that's already what `knife show` emits, it stays grepable,
// and inventing per-type human renderers would diverge from how Chef
// admins read these objects today.
func (p *Printer) Value(v any) error {
	enc := json.NewEncoder(p.w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

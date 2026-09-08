package explore

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	cinc "github.com/cinc-project/cinc-api"
)

// summaryField is one label/value row in the right-hand summary panel.
type summaryField struct {
	Label string
	Value string
}

// summaryView is what the panel renders for the selected object: a curated
// set of human-friendly Fields, plus the object's pretty-printed JSON in
// JSON. The pane shows the Fields; JSON rides along so the detail and edit
// views can reuse this one fetch instead of issuing their own Get. Title,
// when set, overrides the bare object name in the panel heading (a node adds
// its platform, for example).
type summaryView struct {
	Title  string
	Fields []summaryField
	JSON   string
}

// summarize is the single contract every kind summarizes through: fetch the
// object with get, build the heading with title (nil ⇒ bare name) and the
// curated facts with fields, and always carry the object's JSON along for the
// detail/edit views to reuse. Centralizing the fetch-then-render shape here is
// what lets each kind declare only its own get and field builder.
func summarize[T any](
	ctx context.Context, c *cinc.Client, name string,
	get func(context.Context, *cinc.Client, string) (*T, error),
	title func(*T) string,
	fields func(context.Context, *cinc.Client, *T) []summaryField,
) (summaryView, error) {
	obj, err := get(ctx, c, name)
	if err != nil {
		return summaryView{}, err
	}
	body, err := prettyJSON(obj)
	if err != nil {
		return summaryView{}, err
	}
	var heading string
	if title != nil {
		heading = title(obj)
	}
	var fs []summaryField
	if fields != nil {
		fs = fields(ctx, c, obj)
	}
	return summaryView{Title: heading, Fields: fs, JSON: body}, nil
}

// ----- shared value formatters -----------------------------------------
//
// Every per-type field builder renders through these so empty values,
// counts, booleans, and lists read the same way across the whole UI.

// dash is the placeholder shown for an empty field value.
const dash = "—"

// orDash returns s, or the em-dash placeholder when s is empty.
func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return dash
	}
	return s
}

// count renders a quantity as its plain decimal string.
func count(n int) string { return strconv.Itoa(n) }

// yesNo renders a boolean as a human Yes/No.
func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

// presence reports whether a value is set, for fields where the value itself
// is noise (a key blob) but its presence is the fact worth showing.
func presence(s string) string {
	if strings.TrimSpace(s) == "" {
		return "not set"
	}
	return "set"
}

// list renders items as a comma-separated string, collapsing to the em-dash
// when empty and, when max > 0 and there are more than max, trailing the first
// max with a "+N more" count so a long list can't blow out the pane.
func list(items []string, max int) string {
	if len(items) == 0 {
		return dash
	}
	if max > 0 && len(items) > max {
		return strings.Join(items[:max], ", ") + fmt.Sprintf(", +%d more", len(items)-max)
	}
	return strings.Join(items, ", ")
}

// relativeTime renders a Unix epoch (as a float, the shape Chef's
// ohai_time uses) as a short "Nh ago" string relative to now. A
// non-positive epoch means we never saw the node, and anything in the
// future collapses to "just now".
func relativeTime(epoch float64, now time.Time) string {
	if epoch <= 0 {
		return "never"
	}
	d := now.Sub(time.Unix(int64(epoch), 0))
	switch {
	case d < time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

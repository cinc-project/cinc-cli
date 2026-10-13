package explore

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// formatDownloads renders an int as a thousands-separated string
// ("142,330,221"). The supermarket displays it that way and it scans
// faster than raw integers.
func formatDownloads(n int) string {
	if n == 0 {
		return "0"
	}
	s := strconv.Itoa(n)
	neg := s[0] == '-'
	if neg {
		s = s[1:]
	}
	first := len(s) % 3
	if first == 0 {
		first = 3
	}
	var out strings.Builder
	out.WriteString(s[:first])
	for i := first; i < len(s); i += 3 {
		out.WriteString("," + s[i:i+3])
	}
	if neg {
		return "-" + out.String()
	}
	return out.String()
}

// formatRelativeTime turns a timestamp into the kind of phrasing a user
// scans in a list ("2 days ago", "just now"). Anything older than a year
// falls back to the date so we don't claim "428 days ago".
func formatRelativeTime(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hr ago", int(d.Hours()))
	case d < 14*24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "yesterday"
		}
		return fmt.Sprintf("%d days ago", days)
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%d weeks ago", int(d.Hours()/(24*7)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%d months ago", int(d.Hours()/(24*30)))
	}
	return t.Format("2006-01-02")
}

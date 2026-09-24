package rubylit

import "testing"

func TestQuote(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"web", `'web'`},
		{`#{system("id")}`, `'#{system("id")}'`},
		{`it's`, `'it\'s'`},
		{`C:\keys`, `'C:\\keys'`},
		{`\'`, `'\\\''`},
		{"", `''`},
	} {
		if got := Quote(tc.in); got != tc.want {
			t.Errorf("Quote(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

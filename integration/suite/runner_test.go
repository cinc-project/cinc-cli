package suite

import "testing"

// TestHasStatus pins the status-code match the cases' error assertions rely
// on. It needs no server, so it runs with the coverage guard.
func TestHasStatus(t *testing.T) {
	for _, tc := range []struct {
		s    string
		code int
		want bool
	}{
		{"Error: cinc: DELETE /organizations/o/nodes/n: 403: missing delete permission", 403, true},
		{"Error: cinc: GET /nodes/x: 401", 401, true},
		{"401 Unauthorized", 401, true},
		{"server answered (409)", 409, true},
		// A random object name must never read as a status.
		{"Error: cinc: DELETE /organizations/o/nodes/t-node-b4010604: 403: missing delete permission", 401, false},
		{"Error: cinc: PUT /nodes/t-node-401abcde: 403", 401, false},
		{"Error: cinc: PUT /nodes/t-node-abcde401: 403", 401, false},
		{"port 14030", 403, false},
	} {
		if got := hasStatus(tc.s, tc.code); got != tc.want {
			t.Errorf("hasStatus(%q, %d) = %v, want %v", tc.s, tc.code, got, tc.want)
		}
	}
}

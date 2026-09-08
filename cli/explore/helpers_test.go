package explore

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	cinc "github.com/cinc-project/cinc-api"
)

// testClient builds a *cinc.Client for org "acme" pointed at srv.
func testClient(t *testing.T, srv *httptest.Server) *cinc.Client {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	c, err := cinc.NewClient(cinc.Config{
		ServerURL: srv.URL, Org: "acme", ClientName: "tester", Key: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// modelOnKinds returns a model parked on the kind menu with a working
// client, as if a single profile had been resolved. width/height are
// set so list windowing has room.
func modelOnKinds(t *testing.T, c *cinc.Client) model {
	t.Helper()
	m := newModel(context.Background(), Options{}, startup{
		client:       c,
		profileName:  "default",
		screen:       screenKinds,
		profileNames: []string{"default"},
	})
	// The real program always receives a WindowSizeMsg first; it sizes
	// the detail viewport and editor. Mirror that so views render.
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return next.(model)
}

// step applies msg and returns the concrete model plus any command.
func step(t *testing.T, m model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	nm, ok := next.(model)
	if !ok {
		t.Fatalf("Update returned %T, want model", next)
	}
	return nm, cmd
}

// drain executes a single command and returns its message. Commands the
// TUI issues for server calls are plain thunks, so this is enough to
// follow a flow without a running program.
func drain(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

// pressRune feeds a single-rune keypress.
func pressRune(t *testing.T, m model, r rune) (model, tea.Cmd) {
	return step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
}

// pressKey feeds a special key.
func pressKey(t *testing.T, m model, k tea.KeyType) (model, tea.Cmd) {
	return step(t, m, tea.KeyMsg{Type: k})
}

// jsonHandler registers a JSON GET response at path.
func jsonHandler(mux *http.ServeMux, path string, body string) {
	mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
}

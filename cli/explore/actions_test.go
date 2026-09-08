package explore

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// commitEditor drives the embedded JSON editor to commit its current
// buffer. The editor opens in structural mode, which commits immediately
// on Ctrl-D (no preview/confirm round-trip).
func commitEditor(t *testing.T, m model) (model, tea.Cmd) {
	t.Helper()
	return pressKey(t, m, tea.KeyCtrlD)
}

func TestEditFlowSavesEditedObject(t *testing.T) {
	var gotPut string
	mux := http.NewServeMux()
	jsonHandler(mux, "/organizations/acme/nodes", `{"web01":"u"}`)
	mux.HandleFunc("/organizations/acme/nodes/web01", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			b, _ := io.ReadAll(r.Body)
			gotPut = string(b)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"web01","run_list":[]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	m := modelOnKinds(t, testClient(t, srv))
	m, cmd := pressKey(t, m, tea.KeyEnter) // open Nodes
	m, _ = step(t, m, drain(cmd))

	// e starts an edit of the selected node; the seed is fetched async.
	m, cmd = pressRune(t, m, 'e')
	m, _ = step(t, m, drain(cmd)) // editSeedMsg → editor opens
	if m.screen != screenEditor {
		t.Fatalf("screen = %v, want editor", m.screen)
	}
	m, cmd = commitEditor(t, m)
	if m.screen != screenList {
		t.Fatalf("after commit screen = %v, want list", m.screen)
	}
	m, _ = step(t, m, drain(cmd)) // mutationDoneMsg
	if gotPut == "" {
		t.Fatal("expected a PUT to the node")
	}
	if !strings.Contains(m.status, "Updated web01") {
		t.Errorf("status = %q, want Updated web01", m.status)
	}
}

func TestCreateWithSecretShowsResultModal(t *testing.T) {
	mux := http.NewServeMux()
	// One handler serves both the POST (create) and the GET (reload).
	mux.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"uri":"u","chef_key":{"private_key":"SECRET-PEM"}}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	m := modelOnKinds(t, testClient(t, srv))
	m.cur = newUserKind()
	m.screen = screenList

	m, _ = m2(m.startCreate()) // n: Creatable → editor with template
	if m.screen != screenEditor {
		t.Fatalf("screen = %v, want editor", m.screen)
	}
	m, cmd := commitEditor(t, m)
	m, _ = step(t, m, drain(cmd)) // mutationDoneMsg with secret

	if m.screen != screenResult {
		t.Fatalf("screen = %v, want result", m.screen)
	}
	if !strings.Contains(m.resultBody, "SECRET-PEM") {
		t.Errorf("result body missing secret, got %q", m.resultBody)
	}
}

func TestCreateReportsSubmittedNameWhenServerOmitsIt(t *testing.T) {
	mux := http.NewServeMux()
	// Chef's POST /nodes responds with only a URI — no name. The status
	// must still name the created object from the submitted document.
	mux.HandleFunc("/organizations/acme/nodes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"uri":"https://example.test/nodes/new-node"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`)) // list + reload
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	m := modelOnKinds(t, testClient(t, srv))
	m, cmd := pressKey(t, m, tea.KeyEnter) // open Nodes
	m, _ = step(t, m, drain(cmd))

	m, _ = m2(m.startCreate()) // the node form opens, focused on the name
	for _, r := range "new-node" {
		m, _ = pressRune(t, m, r)
	}
	m, cmd = commitEditor(t, m)
	m, _ = step(t, m, drain(cmd)) // mutationDoneMsg
	if !strings.Contains(m.status, "Created new-node") {
		t.Errorf("status = %q, want Created new-node", m.status)
	}
}

func TestDeleteConfirmYesDeletes(t *testing.T) {
	var deleted bool
	mux := http.NewServeMux()
	jsonHandler(mux, "/organizations/acme/roles", `{"web":"u"}`)
	mux.HandleFunc("/organizations/acme/roles/web", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	m := modelOnKinds(t, testClient(t, srv))
	m.kindCursor = 1 // Roles
	m, cmd := pressKey(t, m, tea.KeyEnter)
	m, _ = step(t, m, drain(cmd))

	m, _ = pressRune(t, m, 'd') // delete → confirm
	if m.screen != screenConfirm {
		t.Fatalf("screen = %v, want confirm", m.screen)
	}
	m, cmd = pressRune(t, m, 'y')
	m, _ = step(t, m, drain(cmd)) // mutationDoneMsg
	if !deleted {
		t.Error("expected DELETE on the role")
	}
	if !strings.Contains(m.status, "Deleted web") {
		t.Errorf("status = %q, want Deleted web", m.status)
	}
}

func TestDeleteConfirmNoCancels(t *testing.T) {
	m := modelOnKinds(t, testClient(t, nodesMux(t)))
	m, cmd := pressKey(t, m, tea.KeyEnter)
	m, _ = step(t, m, drain(cmd))

	m, _ = pressRune(t, m, 'd')
	if m.screen != screenConfirm {
		t.Fatalf("screen = %v, want confirm", m.screen)
	}
	m, gotCmd := pressRune(t, m, 'n')
	if m.screen != screenList {
		t.Errorf("screen = %v, want list (cancelled)", m.screen)
	}
	if gotCmd != nil {
		t.Error("cancel should not issue a command")
	}
}

func TestNamedCreateIssuesPost(t *testing.T) {
	var posted bool
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/data", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posted = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	m := modelOnKinds(t, testClient(t, srv))
	m.cur = dataBagKind{}
	m.screen = screenList

	m, _ = m2(m.startCreate()) // NamedCreatable → name modal
	if m.screen != screenName {
		t.Fatalf("screen = %v, want name modal", m.screen)
	}
	m, _ = step(t, m, keyRunes("creds"))
	m, cmd := pressKey(t, m, tea.KeyEnter)
	m, _ = step(t, m, drain(cmd))
	if !posted {
		t.Error("expected POST to create the data bag")
	}
	if !strings.Contains(m.status, "Created creds") {
		t.Errorf("status = %q, want Created creds", m.status)
	}
}

func TestEditIgnoredForNonEditableKind(t *testing.T) {
	m := modelOnKinds(t, testClient(t, nodesMux(t)))
	m.cur = cookbookKind{} // DrillDown only, not Editable
	m.screen = screenList
	m.rows = []Row{{Name: "apache", Cells: []string{"apache", "1"}}}

	got, cmd := m2(m.startEdit("apache"))
	if got.screen != screenList {
		t.Errorf("edit on non-editable kind changed screen to %v", got.screen)
	}
	if cmd != nil {
		t.Error("edit on non-editable kind should issue no command")
	}
}

// m2 adapts a method that returns (tea.Model, tea.Cmd) to the concrete
// (model, tea.Cmd) the tests work with.
func m2(mdl tea.Model, cmd tea.Cmd) (model, tea.Cmd) {
	return mdl.(model), cmd
}

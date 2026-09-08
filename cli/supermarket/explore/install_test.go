package explore

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	sm "github.com/cinc-project/cinc-supermarket-api"
)

func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestInstallKeyStartsConfirmation(t *testing.T) {
	m := newTestModel(t, &fakeClient{})
	m.browse.items = []sm.CookbookSummary{cookbookSummary("nginx", "x")}
	m.browse.cursor = 0

	next, cmd := handleBrowseKey(m, keyRunes("i"))
	got := next.(model).browse
	if !got.confirmInstall {
		t.Fatal("pressing i should start the install confirmation")
	}
	if got.installName != "nginx" {
		t.Errorf("installName = %q, want nginx", got.installName)
	}
	if cmd != nil {
		t.Error("starting the confirmation shouldn't fire a command")
	}
}

func TestInstallKeyIgnoredWithNoItems(t *testing.T) {
	m := newTestModel(t, &fakeClient{})
	next, _ := handleBrowseKey(m, keyRunes("i"))
	if next.(model).browse.confirmInstall {
		t.Error("install confirmation should not start with an empty list")
	}
}

func TestInstallConfirmYesRunsInstall(t *testing.T) {
	var gotName, gotVersion string
	m := newTestModel(t, &fakeClient{})
	m.install = func(_ context.Context, name, version string) error {
		gotName, gotVersion = name, version
		return nil
	}
	m.browse.confirmInstall = true
	m.browse.installName = "nginx"

	next, cmd := handleBrowseKey(m, keyRunes("y"))
	got := next.(model).browse
	if got.confirmInstall {
		t.Error("confirmation should be dismissed after y")
	}
	if !got.installing {
		t.Error("installing should be true while the install runs")
	}
	if cmd == nil {
		t.Fatal("y should fire the install command")
	}
	msg := runCmd(t, cmd).(installDoneMsg)
	if msg.err != nil {
		t.Fatalf("install msg err = %v", msg.err)
	}
	if gotName != "nginx" || gotVersion != "" {
		t.Errorf("install called with (%q, %q), want (nginx, latest=\"\")", gotName, gotVersion)
	}
}

func TestInstallConfirmNoCancels(t *testing.T) {
	called := false
	m := newTestModel(t, &fakeClient{})
	m.install = func(context.Context, string, string) error {
		called = true
		return nil
	}
	m.browse.confirmInstall = true
	m.browse.installName = "nginx"

	next, cmd := handleBrowseKey(m, keyRunes("n"))
	if next.(model).browse.confirmInstall {
		t.Error("n should cancel the confirmation")
	}
	if cmd != nil {
		t.Error("n should not fire a command")
	}
	if called {
		t.Error("install should not run when cancelled")
	}
}

func TestInstallDoneSuccessShowsMessage(t *testing.T) {
	m := newTestModel(t, &fakeClient{})
	m.browse.installing = true

	next, _ := m.Update(installDoneMsg{name: "nginx", version: "1.2.0"})
	got := next.(model).browse
	if got.installing {
		t.Error("installing should be cleared when done")
	}
	if got.installErr != "" {
		t.Errorf("installErr = %q, want empty", got.installErr)
	}
	if got.installMsg == "" {
		t.Error("a success message should be shown")
	}
}

func TestInstallDoneSurfacesError(t *testing.T) {
	m := newTestModel(t, &fakeClient{})
	m.browse.installing = true

	next, _ := m.Update(installDoneMsg{name: "nginx", err: errors.New("no credentials")})
	got := next.(model).browse
	if got.installing {
		t.Error("installing should be cleared on error")
	}
	if got.installErr != "no credentials" {
		t.Errorf("installErr = %q, want \"no credentials\"", got.installErr)
	}
}

func TestFooterShowsConfirmPrompt(t *testing.T) {
	m := newTestModel(t, &fakeClient{})
	m.browse.confirmInstall = true
	m.browse.installName = "nginx"

	footer := renderFooter(m)
	if !strings.Contains(footer, "nginx") || !strings.Contains(footer, "y/n") {
		t.Errorf("footer = %q, want an install confirm prompt for nginx", footer)
	}
}

func TestFooterShowsInstallResult(t *testing.T) {
	m := newTestModel(t, &fakeClient{})
	m.browse.installMsg = "Installed nginx onto the server."
	if !strings.Contains(renderFooter(m), "Installed nginx") {
		t.Error("footer should show the install success message")
	}

	m = newTestModel(t, &fakeClient{})
	m.browse.installErr = "no credentials"
	if !strings.Contains(renderFooter(m), "no credentials") {
		t.Error("footer should show the install error")
	}
}

func TestFooterIncludesInstallHint(t *testing.T) {
	m := newTestModel(t, &fakeClient{})
	if !strings.Contains(renderFooter(m), "install") {
		t.Error("footer help should advertise the install key")
	}
}

func TestInstallConfirmWithoutInstallerShowsError(t *testing.T) {
	m := newTestModel(t, &fakeClient{}) // install is nil
	m.browse.confirmInstall = true
	m.browse.installName = "nginx"

	next, cmd := handleBrowseKey(m, keyRunes("y"))
	got := next.(model).browse
	if got.installing {
		t.Error("installing should not start without an installer")
	}
	if got.installErr == "" {
		t.Error("a missing installer should surface an error")
	}
	if cmd != nil {
		t.Error("no command should fire without an installer")
	}
}

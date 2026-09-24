package suite

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// tui is the cinc binary running on a pseudo-terminal, for driving
// `cinc explore` the way a person at a keyboard would.
type tui struct {
	t    *testing.T
	ptmx *os.File
	cmd  *exec.Cmd
	done chan error

	mu   sync.Mutex
	out  bytes.Buffer // everything the program has drawn
	mark int          // waitFor only looks at output after this offset
}

// tuiTimeout bounds how long a TUI step may take to show its result.
const tuiTimeout = 20 * time.Second

// startTUI runs the binary on a 200x50 pseudo-terminal, with the case's
// HOME and environment, and stops it when the case ends.
func (c *cli) startTUI(args ...string) *tui {
	c.t.Helper()
	cmd := exec.Command(c.bin, args...)
	cmd.Dir = c.home
	cmd.Env = append(append(c.baseEnv(), c.env...), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 50, Cols: 200})
	if err != nil {
		c.t.Fatalf("start cinc %s on a pty: %v", strings.Join(args, " "), err)
	}
	u := &tui{t: c.t, ptmx: ptmx, cmd: cmd, done: make(chan error, 1)}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				u.mu.Lock()
				u.out.Write(buf[:n])
				u.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	go func() { u.done <- cmd.Wait() }()
	c.t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = ptmx.Close()
	})
	return u
}

// ansi matches the terminal control sequences a TUI draws with: CSI, OSC
// and the two-byte escapes.
var ansi = regexp.MustCompile(`\x1b\[[0-9;?<=>]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[()][0-9A-Za-z]|\x1b[=>78DEHMNOZc]`)

// screen returns what the program has drawn since the last send, with the
// control sequences stripped.
func (u *tui) screen() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return ansi.ReplaceAllString(u.out.String()[u.mark:], "")
}

// send types each of keys in turn, and moves the mark so waitFor sees only
// what they drew. Each one is written on its own, a moment apart: bubbletea
// reads the runes that arrive together as one key message, so "/" followed
// at once by a name would read as the name rather than the filter key.
func (u *tui) send(keys ...string) {
	u.t.Helper()
	u.mu.Lock()
	u.mark = u.out.Len()
	u.mu.Unlock()
	for i, k := range keys {
		if i > 0 {
			time.Sleep(150 * time.Millisecond)
		}
		u.press(k)
	}
}

// press types keys without moving the mark.
func (u *tui) press(keys string) {
	u.t.Helper()
	if _, err := io.WriteString(u.ptmx, keys); err != nil {
		u.t.Fatalf("write %q to the pty: %v", keys, err)
	}
}

// waitFor waits until the program has drawn want since the last send.
func (u *tui) waitFor(want string) {
	u.t.Helper()
	deadline := time.Now().Add(tuiTimeout)
	for !strings.Contains(u.screen(), want) {
		if time.Now().After(deadline) {
			u.t.Fatalf("explore never drew %q; since the last keys it drew:\n%s", want, u.screen())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// selected is how explore draws the highlighted row of a list or menu.
func selected(label string) string { return "▶ " + label }

// quit presses q and waits for a clean exit.
func (u *tui) quit() {
	u.t.Helper()
	u.send("q")
	select {
	case err := <-u.done:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			u.t.Fatalf("explore exited %d after q:\n%s", ee.ExitCode(), u.screen())
		} else if err != nil {
			u.t.Fatalf("explore: %v", err)
		}
	case <-time.After(tuiTimeout):
		u.t.Fatalf("explore did not exit after q:\n%s", u.screen())
	}
}

//go:build unix

package suite

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"syscall"
)

// execDetached runs the binary like exec, but in a new session with no
// controlling terminal, as under cron or CI. A command that wants a terminal
// then cannot fall back to /dev/tty, which would otherwise take over the
// terminal of whoever runs the suite and hang the case.
func (c *cli) execDetached(args ...string) result {
	c.t.Helper()
	cmd := exec.Command(c.bin, args...)
	cmd.Dir = c.home
	cmd.Env = append(c.baseEnv(), c.env...)
	cmd.Stdin = strings.NewReader("")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	r := result{args: args, stdout: stdout.String(), stderr: stderr.String()}
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		r.exitCode = ee.ExitCode()
	case err != nil:
		c.t.Fatalf("running cinc %s: %v", strings.Join(args, " "), err)
	}
	return r
}

//go:build unix

package suite

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// execWithoutTerminal runs the binary as c.exec does, but in a new session,
// so it has no controlling terminal even when the suite is run from one. A
// command that would open an interactive editor then sees what it sees in
// a script or CI job, instead of taking over the developer's terminal. The
// run is killed after a minute, so a command that waits for input anyway
// fails instead of hanging the suite.
func execWithoutTerminal(c *cli, args ...string) result {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.bin, args...)
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
	case ctx.Err() != nil:
		c.t.Fatalf("cinc %s waited for input without a terminal", strings.Join(args, " "))
	case errors.As(err, &ee):
		r.exitCode = ee.ExitCode()
	case err != nil:
		c.t.Fatalf("running cinc %s: %v", strings.Join(args, " "), err)
	}
	return r
}

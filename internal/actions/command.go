package actions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/perm"
)

const (
	commandTimeout = 5 * time.Minute
	commandOutput  = 6000 // bytes of output kept (the tail)
)

// runCommand runs one allowed command line in the project folder, without a
// shell, and returns its (tail of) combined output.
func runCommand(ctx context.Context, root, line string) (string, error) {
	args, err := perm.SplitCommand(line)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	runErr := cmd.Run()
	text := strings.TrimSpace(out.String())
	if len(text) > commandOutput {
		text = "…" + text[len(text)-commandOutput:]
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return text, fmt.Errorf("quá %s, đã dừng lệnh", commandTimeout)
	}
	var exit *exec.ExitError
	if errors.As(runErr, &exit) {
		return text, fmt.Errorf("thoát với mã %d", exit.ExitCode())
	}
	return text, runErr
}

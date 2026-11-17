// Command unboxed is the command line entry point.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/urfave/cli/v3"
)

// Exit codes. Anything a supervisor might branch on lives here rather than
// being scattered as literals.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

// run is the whole program, parameterised on its arguments and output streams
// so it can be exercised from tests without a process boundary.
func run(args []string, stdout, stderr io.Writer) int {
	log := slog.New(slog.NewTextHandler(stderr, nil))

	cmd := &cli.Command{
		Name:      "unboxed",
		Usage:     "connect to Schoolbox with your AI agents via MCP",
		Version:   buildVersion(),
		ArgsUsage: " ",
		Writer:    stdout,
		ErrWriter: stderr,
		Action: func(_ context.Context, cmd *cli.Command) error {
			if cmd.Args().Present() {
				return cli.Exit(
					fmt.Sprintf("unboxed takes no arguments, got %q", cmd.Args().Slice()),
					exitUsage)
			}
			_, err := fmt.Fprintln(cmd.Writer, "hello from unboxed")
			return err
		},
		// A usage error is the operator's mistake, not a runtime failure, and
		// gets its own exit code.
		OnUsageError: func(_ context.Context, _ *cli.Command, err error, _ bool) error {
			return cli.Exit(err.Error(), exitUsage)
		},
		// The default handler writes to a package-global writer and calls
		// os.Exit, which would make run untestable without a process boundary.
		// Reporting and exit-code mapping happen below instead.
		ExitErrHandler: func(_ context.Context, _ *cli.Command, _ error) {},
	}

	// os.Interrupt and syscall.SIGTERM are both defined on Windows, so no
	// build-tagged signal handling is needed.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := cmd.Run(ctx, args)
	if err == nil {
		return exitOK
	}

	if exit, ok := errors.AsType[cli.ExitCoder](err); ok {
		if msg := exit.Error(); msg != "" {
			fmt.Fprintln(stderr, msg) //nolint:errcheck // nothing useful to do if stderr is gone
		}
		return exit.ExitCode()
	}
	log.Error("command failed", slog.Any("error", err))
	return exitFailure
}

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"udpreflector/internal/application"
	"udpreflector/internal/buildinfo"
	"udpreflector/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(execute(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func execute(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		printHelp(stdout)
		return 0
	}

	if len(args) == 1 && args[0] == "--version" {
		fmt.Fprintf(stdout, "udpreflector %s\n", buildinfo.Identifier)
		return 0
	}

	configuration, err := config.Parse(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printHelp(stdout)
			return 0
		}
		fmt.Fprintf(stderr, "invalid configuration: %v\n", err)
		return 2
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil))
	if err := application.Run(ctx, configuration, logger); err != nil {
		logger.Error("UDP reflector failed", "error", err)
		return 1
	}
	return 0
}

func printHelp(stdout io.Writer) {
	fmt.Fprint(stdout, `Usage: udpreflector [flags]

Reflect UDP packets to other recent peers on each configured UDP port.

Flags:
  -help
        show this help and exit
  -version
        show build identifier and exit
`)
	config.PrintDefaults(stdout)
}

package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"testing"
)

func TestExecuteRejectsInvalidConfiguration(t *testing.T) {
	var stderr bytes.Buffer
	exitCode := execute(context.Background(), nil, io.Discard, &stderr)

	if exitCode != 2 {
		t.Fatalf("execute() exit code = %d, want 2", exitCode)
	}
	if !strings.Contains(stderr.String(), "--port-range is required") {
		t.Fatalf("stderr does not explain invalid configuration: %q", stderr.String())
	}
}

func TestExecutePrintsVersionWithoutConfiguration(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := execute(context.Background(), []string{"--version"}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("execute() exit code = %d, want 0", exitCode)
	}
	if stdout.String() != "udpreflector dev\n" {
		t.Fatalf("stdout = %q, want development version", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty output", stderr.String())
	}
}

func TestExecutePrintsHelpWithoutConfiguration(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "long help flag", args: []string{"--help"}},
		{name: "short help flag", args: []string{"-h"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := execute(context.Background(), test.args, &stdout, &stderr)

			if exitCode != 0 {
				t.Fatalf("execute() exit code = %d, want 0", exitCode)
			}
			if !strings.Contains(stdout.String(), "Usage: udpreflector [flags]") {
				t.Fatalf("stdout does not contain usage text: %q", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty output", stderr.String())
			}
		})
	}
}

func TestExecuteReportsStartupFailure(t *testing.T) {
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy metrics address: %v", err)
	}
	defer occupied.Close()

	var stderr bytes.Buffer
	exitCode := execute(context.Background(), []string{
		"--port-range=40000-40000",
		"--udp4-address=127.0.0.1",
		"--udp6-address=::1",
		"--metrics-address=" + occupied.Addr().String(),
	}, io.Discard, &stderr)

	if exitCode != 1 {
		t.Fatalf("execute() exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "UDP reflector failed") {
		t.Fatalf("stderr does not report startup failure: %q", stderr.String())
	}
}

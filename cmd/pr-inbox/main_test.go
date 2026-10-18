package main

import (
	"os"
	"strings"
	"testing"

	"github.com/cpanato/pr-inbox/internal/tui"
)

func TestTUIModeRejectsDoctorBeforeConnecting(t *testing.T) {
	for _, args := range [][]string{{"doctor", "--tui"}, {"--tui", "--doctor"}} {
		if err := run(args); err == nil || !strings.Contains(err.Error(), "--tui cannot be combined with doctor") {
			t.Fatalf("run(%v)=%v", args, err)
		}
	}
}

func TestTUIModeRejectsRedirectedIOBeforeConnecting(t *testing.T) {
	if tui.Interactive(os.Stdin, os.Stdout) {
		t.Skip("test requires redirected test output")
	}
	if err := run([]string{"--tui"}); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("non-interactive run=%v", err)
	}
}

package exec

import (
	"context"
	"strings"
	"testing"
)

func TestOSRunner_Run_Success(t *testing.T) {
	r := OSRunner{}
	if err := r.Run(context.Background(), "true"); err != nil {
		t.Fatalf("run true: %v", err)
	}
}

func TestOSRunner_Run_NonZeroExit(t *testing.T) {
	r := OSRunner{}
	err := r.Run(context.Background(), "false")
	if err == nil {
		t.Fatal("expected non-nil error from false")
	}
	if !strings.Contains(err.Error(), "false") {
		t.Fatalf("error should mention command name, got: %v", err)
	}
}

func TestOSRunner_Output_ReturnsStdout(t *testing.T) {
	r := OSRunner{}
	out, err := r.Output(context.Background(), "printf", "hello")
	if err != nil {
		t.Fatalf("output printf: %v", err)
	}
	if string(out) != "hello" {
		t.Fatalf("want %q, got %q", "hello", string(out))
	}
}

func TestOSRunner_Output_WrapsStderr(t *testing.T) {
	r := OSRunner{}
	// `sh -c 'echo oops 1>&2; exit 1'` — deterministic stderr + nonzero exit.
	_, err := r.Output(context.Background(), "sh", "-c", "echo oops 1>&2; exit 1")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "oops") {
		t.Fatalf("error should contain stderr tail, got: %v", err)
	}
}

func TestOSRunner_LookPath_Missing(t *testing.T) {
	r := OSRunner{}
	if _, err := r.LookPath("definitely-not-a-binary-xyzzy"); err == nil {
		t.Fatal("expected LookPath to fail for missing binary")
	}
}

func TestOSRunner_LookPath_Found(t *testing.T) {
	r := OSRunner{}
	path, err := r.LookPath("sh")
	if err != nil {
		t.Fatalf("look sh: %v", err)
	}
	if path == "" {
		t.Fatal("empty path for sh")
	}
}

func TestOSRunner_Start_ReturnsQuickly(t *testing.T) {
	r := OSRunner{}
	// `true` exits immediately; Start must not block waiting for it.
	if err := r.Start("true", nil, false); err != nil {
		t.Fatalf("start true: %v", err)
	}
}

func TestOSRunner_Start_MissingBinary(t *testing.T) {
	r := OSRunner{}
	err := r.Start("definitely-not-a-binary-xyzzy", nil, false)
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestDefault_IsOSRunner(t *testing.T) {
	if _, ok := Default.(OSRunner); !ok {
		t.Fatalf("Default should be OSRunner, got %T", Default)
	}
}

func TestTrimTail(t *testing.T) {
	cases := map[string]string{
		"hello\n":      "hello",
		"hello\r\n":    "hello",
		"hello\n\n\n":  "hello",
		"hello":        "hello",
		"":             "",
		"\n":           "",
		"line1\nline2": "line1\nline2",
	}
	for in, want := range cases {
		if got := trimTail([]byte(in)); got != want {
			t.Errorf("trimTail(%q) = %q, want %q", in, got, want)
		}
	}
}

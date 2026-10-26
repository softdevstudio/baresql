package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/softdevstudio/baresql"
)

func TestCLIVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	if got, want := out.String(), "baresql "+baresql.Version+"\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCLIUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"frobnicate"}, &out, &errb); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errb.String(), `unknown command "frobnicate"`) {
		t.Fatalf("stderr %q", errb.String())
	}
}

func TestCLINoArguments(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "Usage:") {
		t.Fatalf("no usage on stderr: %q", errb.String())
	}
}

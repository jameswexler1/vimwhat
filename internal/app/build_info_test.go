package app

import (
	"bytes"
	"strings"
	"testing"
)

func TestFormatBuildInfo(t *testing.T) {
	got := formatBuildInfo(BuildInfo{
		Version:  "v1.2.3",
		Revision: "abc123",
		Time:     "2026-07-18T20:00:00Z",
		Modified: true,
	})
	want := "version=v1.2.3 revision=abc123 time=2026-07-18T20:00:00Z modified=true"
	if got != want {
		t.Fatalf("formatBuildInfo() = %q, want %q", got, want)
	}
}

func TestRunVersion(t *testing.T) {
	var stdout bytes.Buffer
	if code := run(Environment{}, []string{"version"}, &stdout, &bytes.Buffer{}); code != 0 {
		t.Fatalf("run(version) = %d, want 0", code)
	}
	if output := stdout.String(); !strings.HasPrefix(output, "vimwhat version=") {
		t.Fatalf("version output = %q", output)
	}
}

func TestVersionCommandRequiresOneArgument(t *testing.T) {
	for _, test := range []struct {
		args []string
		want bool
	}{
		{args: []string{"version"}, want: true},
		{args: []string{"--version"}, want: true},
		{args: []string{"version", "extra"}, want: false},
		{args: nil, want: false},
	} {
		if got := isVersionCommand(test.args); got != test.want {
			t.Fatalf("isVersionCommand(%q) = %v, want %v", test.args, got, test.want)
		}
	}
}

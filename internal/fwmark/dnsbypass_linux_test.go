//go:build linux

package fwmark

import (
	"strings"
	"testing"
)

func TestInsertToDeleteStripsPosition(t *testing.T) {
	insert := []string{"-t", "mangle", "-I", "shellcrash_mark_out", "1", "-m", "mark", "--mark", "0x5624a328", "-j", "RETURN"}
	got := insertToDelete(insert)
	want := []string{"-t", "mangle", "-D", "shellcrash_mark_out", "-m", "mark", "--mark", "0x5624a328", "-j", "RETURN"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("delete = %v, want %v", got, want)
	}
}

func TestEnsureBypassPutsMarkReturnOnShellCrashMarkChains(t *testing.T) {
	var calls []string
	run := func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}
	ensureBypassWith(run, "0x5624a328")

	wantHeads := []string{
		"-t mangle -D shellcrash_mark_out -m mark --mark 0x5624a328 -j RETURN",
		"-t mangle -I shellcrash_mark_out 1 -m mark --mark 0x5624a328 -j RETURN",
		"-t mangle -D shellcrash_mark -m mark --mark 0x5624a328 -j RETURN",
		"-t mangle -I shellcrash_mark 1 -m mark --mark 0x5624a328 -j RETURN",
		"-t mangle -D OUTPUT -m mark --mark 0x5624a328 -j RETURN",
		"-t mangle -I OUTPUT 1 -m mark --mark 0x5624a328 -j RETURN",
		"-t nat -D shellcrash_dns_out -m mark --mark 0x5624a328 -j RETURN",
		"-t nat -I shellcrash_dns_out 1 -m mark --mark 0x5624a328 -j RETURN",
	}
	joined := strings.Join(calls, "\n")
	for _, want := range wantHeads {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in calls:\n%s", want, joined)
		}
	}
}

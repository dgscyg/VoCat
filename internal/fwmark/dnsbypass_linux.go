//go:build linux

package fwmark

import (
	"fmt"
	"os/exec"
)

// EnsureDNSBypass inserts iptables RETURN rules so marked export-proxy sockets
// are not hijacked by Clash/ShellCrash. ShellCrash REDIRECTs :53 from the
// cellular source to 1053, and may also REDIRECT other TCP (public IP probes,
// SOCKS CONNECT) into its redir port. It already exempts mark 0x1ed6; we
// exempt VoCat's SO_MARK the same way, for every ShellCrash chain plus a
// catch-all at the top of nat/mangle OUTPUT.
func EnsureDNSBypass(mark uint32) {
	if mark == 0 {
		return
	}
	markText := fmt.Sprintf("0x%x", mark)
	for _, bin := range []string{"iptables", "iptables-nft"} {
		path, err := exec.LookPath(bin)
		if err != nil {
			continue
		}
		for _, table := range []string{"nat", "mangle"} {
			insertIfMissing(path,
				[]string{"-t", table, "-C", "OUTPUT", "-m", "mark", "--mark", markText, "-j", "RETURN"},
				[]string{"-t", table, "-I", "OUTPUT", "1", "-m", "mark", "--mark", markText, "-j", "RETURN"})
		}
		for _, chain := range []string{
			"shellcrash_dns_out", "shellcrash_dns",
			"shellcrash_out", "shellcrash_output", "shellcrash",
		} {
			insertIfMissing(path,
				[]string{"-t", "nat", "-C", chain, "-m", "mark", "--mark", markText, "-j", "RETURN"},
				[]string{"-t", "nat", "-I", chain, "1", "-m", "mark", "--mark", markText, "-j", "RETURN"})
		}
		for _, proto := range []string{"udp", "tcp"} {
			check := []string{"-t", "nat", "-C", "OUTPUT", "-p", proto, "-m", proto, "--dport", "53", "-m", "mark", "--mark", markText, "-j", "RETURN"}
			insert := []string{"-t", "nat", "-I", "OUTPUT", "1", "-p", proto, "-m", proto, "--dport", "53", "-m", "mark", "--mark", markText, "-j", "RETURN"}
			insertIfMissing(path, check, insert)
		}
	}
}

func insertIfMissing(bin string, check, insert []string) {
	if exec.Command(bin, check...).Run() == nil {
		return
	}
	_, _ = exec.Command(bin, insert...).CombinedOutput()
}

//go:build linux

package fwmark

import (
	"fmt"
	"os/exec"
)

// clashBypassChains are ShellCrash/Clash/Mihomo chains that hijack DNS or
// rewrite skb->mark. VoCat's SO_MARK must RETURN at the head of each of them.
// nat OUTPUT RETURN alone is not enough: Clash reload inserts
// `-j shellcrash_mark_out` at OUTPUT position 1, and that chain does
// `--set-xmark 0x1ed4` on the cellular source, wiping 0x56xxxxxx and sending
// the packet into table 100 (`local default dev lo`).
var clashBypassChains = []struct {
	table, chain string
}{
	{"nat", "OUTPUT"},
	{"mangle", "OUTPUT"},
	{"nat", "shellcrash_dns_out"},
	{"nat", "shellcrash_dns"},
	{"nat", "shellcrash_out"},
	{"nat", "shellcrash_output"},
	{"nat", "shellcrash"},
	{"mangle", "shellcrash_mark_out"},
	{"mangle", "shellcrash_mark"},
	{"mangle", "shellcrash_out"},
	{"nat", "clash"},
	{"mangle", "clash"},
	{"nat", "mihomo"},
	{"mangle", "mihomo"},
}

// EnsureDNSBypass inserts iptables RETURN rules so marked export-proxy sockets
// are not hijacked by Clash/ShellCrash. ShellCrash REDIRECTs :53 from the
// cellular source to 1053, and may also MARK+TPROXY other TCP (public IP
// probes, SOCKS CONNECT) into its redir port. It already exempts mark 0x1ed6;
// we exempt VoCat's SO_MARK the same way, and keep the RETURN at the head of
// each chain so a Clash reload cannot bury it behind a jump/MARK.
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
		ensureBypassWith(func(args ...string) error {
			return exec.Command(path, args...).Run()
		}, markText)
	}
}

func ensureBypassWith(run func(args ...string) error, markText string) {
	for _, item := range clashBypassChains {
		ensureMarkReturnAtHead(run, item.table, item.chain, markText)
	}
	for _, proto := range []string{"udp", "tcp"} {
		ensureRuleAtHead(run, []string{
			"-t", "nat", "-I", "OUTPUT", "1",
			"-p", proto, "-m", proto, "--dport", "53",
			"-m", "mark", "--mark", markText, "-j", "RETURN",
		})
	}
}

func ensureMarkReturnAtHead(run func(args ...string) error, table, chain, markText string) {
	ensureRuleAtHead(run, []string{
		"-t", table, "-I", chain, "1",
		"-m", "mark", "--mark", markText, "-j", "RETURN",
	})
}

// ensureRuleAtHead deletes a previous copy (if any) then inserts at position 1.
// `-C` + skip would leave a RETURN buried under a Clash jump after reload.
func ensureRuleAtHead(run func(args ...string) error, insert []string) {
	del := insertToDelete(insert)
	if len(del) == 0 {
		return
	}
	_ = run(del...)
	_ = run(insert...)
}

func insertToDelete(insert []string) []string {
	// -t TABLE -I CHAIN 1 REST...  →  -t TABLE -D CHAIN REST...
	if len(insert) < 6 || insert[0] != "-t" || insert[2] != "-I" || insert[4] != "1" {
		return nil
	}
	del := make([]string, 0, len(insert)-1)
	del = append(del, insert[:2]...)
	del = append(del, "-D", insert[3])
	del = append(del, insert[5:]...)
	return del
}

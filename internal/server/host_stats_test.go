package server

import (
	"testing"
)

func TestParseCPUTimes(t *testing.T) {
	times, ok := parseCPUTimes("cpu  38073 0 24013 6762971 3121 0 3019 0 0 0")
	if !ok {
		t.Fatal("parseCPUTimes rejected a valid cpu line")
	}
	wantTotal := uint64(38073 + 0 + 24013 + 6762971 + 3121 + 0 + 3019 + 0)
	if times.total != wantTotal {
		t.Fatalf("total = %d, want %d", times.total, wantTotal)
	}
	if wantIdle := uint64(6762971 + 3121); times.idle != wantIdle {
		t.Fatalf("idle = %d, want %d", times.idle, wantIdle)
	}
	if _, ok := parseCPUTimes("cpu0 1 2 3 4 5 6 7 8"); ok {
		t.Fatal("per-core line must not parse as the aggregate line")
	}
	if _, ok := parseCPUTimes("cpu 1 2 3"); ok {
		t.Fatal("truncated cpu line must not parse")
	}
}

func TestCPUDelta(t *testing.T) {
	prev := hostCPUTimes{idle: 100, total: 200}
	next := hostCPUTimes{idle: 150, total: 300}
	busy, total := cpuDelta(prev, next)
	if busy != 50 || total != 100 {
		t.Fatalf("cpuDelta = (%d, %d), want (50, 100)", busy, total)
	}
	if busy, total := cpuDelta(next, prev); busy != 0 || total != 0 {
		t.Fatalf("backwards counters must report zero, got (%d, %d)", busy, total)
	}
}

func TestParseMeminfo(t *testing.T) {
	content := "MemTotal:        2040424 kB\nMemFree:          920864 kB\nMemAvailable:    1543480 kB\nBuffers:          315908 kB\n"
	total, available, ok := parseMeminfo(content)
	if !ok {
		t.Fatal("parseMeminfo rejected valid content")
	}
	if total != 2040424*1024 {
		t.Fatalf("total = %d, want %d", total, 2040424*1024)
	}
	if available != 1543480*1024 {
		t.Fatalf("available = %d, want %d", available, 1543480*1024)
	}
}

func TestParseNetDevCounters(t *testing.T) {
	content := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:      10       1    0    0    0     0          0         0       20       2    0    0    0     0       0          0
  eth0:     100       1    0    0    0     0          0         0      200       2    0    0    0     0       0          0
br-lan:    1000       1    0    0    0     0          0         0     2000       2    0    0    0     0       0          0
  utun:     300       1    0    0    0     0          0         0      400       2    0    0    0     0       0          0
vocat50a684ceb0: 500 1    0    0    0     0          0         0      600       2    0    0    0     0       0          0
 wwan0:     700       1    0    0    0     0          0         0      800       2    0    0    0     0       0          0
`
	rx, tx := parseNetDevCounters(content)
	// Only eth0 and wwan0 count; lo, br-lan, utun and vocat are virtual.
	if rx != 800 || tx != 1000 {
		t.Fatalf("rx,tx = %d,%d, want 800,1000", rx, tx)
	}
}

func TestHostNetInterfaceCounted(t *testing.T) {
	counted := []string{"eth0", "eth1", "wwan0", "usb0", "wlan0", "enp3s0", "pppoe-wan"}
	for _, name := range counted {
		if !hostNetInterfaceCounted(name) {
			t.Fatalf("%s should be counted", name)
		}
	}
	skipped := []string{"lo", "br-lan", "docker0", "veth123", "ip6tnl0", "sit0", "utun", "vocat50a684ceb0", "wg0", "tun0", "tailscale0", ""}
	for _, name := range skipped {
		if hostNetInterfaceCounted(name) {
			t.Fatalf("%s should be skipped", name)
		}
	}
}

func TestParseCPUInfoModelX86(t *testing.T) {
	content := "processor\t: 0\nvendor_id\t: GenuineIntel\nmodel name\t: Intel(R) Core(TM) i5-6200U CPU @ 2.30GHz\n"
	if model := parseCPUInfoModel(content); model != "Intel(R) Core(TM) i5-6200U CPU @ 2.30GHz" {
		t.Fatalf("model = %q", model)
	}
}

func TestParseCPUInfoARM(t *testing.T) {
	content := "processor\t: 0\nBogoMIPS\t: 48.00\nCPU implementer\t: 0x41\nCPU part\t: 0xd03\nprocessor\t: 1\nCPU part\t: 0xd03\n"
	if model := parseCPUInfoModel(content); model != "" {
		t.Fatalf("ARM cpuinfo must not report an x86 model name, got %q", model)
	}
	part, processors := parseCPUInfoPart(content)
	if part != "0xd03" || processors != 2 {
		t.Fatalf("part,processors = %q,%d, want 0xd03,2", part, processors)
	}
}

func TestParseCompatibleSoC(t *testing.T) {
	raw := "xunlong,orangepi-zero3\x00allwinner,sun50i-h618\x00"
	if soc := parseCompatibleSoC(raw); soc != "Allwinner sun50i-h618" {
		t.Fatalf("soc = %q", soc)
	}
	if soc := parseCompatibleSoC(""); soc != "" {
		t.Fatalf("empty compatible must yield empty soc, got %q", soc)
	}
}

func TestComposeARMCPUModel(t *testing.T) {
	model := composeARMCPUModel("Allwinner sun50i-h618", "0xd03", 4)
	if model != "Allwinner sun50i-h618 · 4× Cortex-A53" {
		t.Fatalf("model = %q", model)
	}
	if model := composeARMCPUModel("", "", 0); model != "" {
		t.Fatalf("empty inputs must yield empty model, got %q", model)
	}
}

func TestParseDmidecodeMemory(t *testing.T) {
	output := `# dmidecode 3.3
Getting SMBIOS data from sysfs.
SMBIOS 3.0 present.

Handle 0x0010, DMI type 17, 40 bytes
Memory Device
	Array Handle: 0x000F
	Error Information Handle: Not Provided
	Total Width: 64 bits
	Data Width: 64 bits
	Size: 8 GB
	Form Factor: SODIMM
	Type: DDR4
	Speed: 2400 MT/s
	Manufacturer: Samsung
	Serial Number: 12345678
	Part Number: M471A1K43CB1-CRC
	Rank: 1

Handle 0x0011, DMI type 17, 40 bytes
Memory Device
	Size: No Module Installed
	Type: Unknown
`
	if model := parseDmidecodeMemory(output); model != "8 GB DDR4 M471A1K43CB1-CRC" {
		t.Fatalf("model = %q", model)
	}
	if model := parseDmidecodeMemory("Memory Device\n\tSize: No Module Installed\n"); model != "" {
		t.Fatalf("unpopulated slots must yield empty model, got %q", model)
	}
}

func TestClampPercent(t *testing.T) {
	if clampPercent(-1) != 0 || clampPercent(101) != 100 || clampPercent(50) != 50 {
		t.Fatal("clampPercent bounds violated")
	}
}

func TestParseUintTrims(t *testing.T) {
	if value, ok := parseUint(" 61440000 "); !ok || value != 61440000 {
		t.Fatalf("parseUint = %d,%v", value, ok)
	}
	if _, ok := parseUint("not-a-number"); ok {
		t.Fatal("parseUint accepted garbage")
	}
}

func TestSkipHostDiskPrefixes(t *testing.T) {
	skipped := []string{"loop0", "ram0", "zram0", "sr0", "nbd0", "dm-0", "md0", "mtdblock0", "ubiblock0_0"}
	for _, name := range skipped {
		if !skipHostDisk(name) {
			t.Fatalf("%s should be skipped", name)
		}
	}
	kept := []string{"sda", "nvme0n1", "mmcblk0", "vda", "sdb"}
	for _, name := range kept {
		if skipHostDisk(name) {
			t.Fatalf("%s should be kept", name)
		}
	}
}

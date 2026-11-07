package gui

import "testing"

func TestParseSampleAndDelta(t *testing.T) {
	s1 := parseSample([]byte(`webmail
Linux
cpu  100 0 50 850 0 0 0 0 0 0
MemTotal:        8000000 kB
MemAvailable:    4000000 kB
1000.00 900.00
@NET
Inter-|   Receive
 face |bytes
  eth0: 1000 0 0 0 0 0 0 0 2000 0 0 0 0 0 0 0
    lo: 999999 0 0 0 0 0 0 0 999999 0 0 0 0 0 0 0
@DISK
Filesystem 1024-blocks Used Available Capacity Mounted on
/dev/sda1 100000 50000 50000 50% /
`))
	if !s1.ok || s1.host != "webmail" || s1.os != "Linux" {
		t.Fatalf("head parse: %+v", s1)
	}
	if s1.cpuTotal != 1000 || s1.cpuIdle != 850 {
		t.Fatalf("cpu: total=%d idle=%d want 1000/850", s1.cpuTotal, s1.cpuIdle)
	}
	if s1.memTotalKiB != 8000000 || s1.memAvailKiB != 4000000 {
		t.Fatalf("mem: %d/%d", s1.memTotalKiB, s1.memAvailKiB)
	}
	if s1.netRx != 1000 || s1.netTx != 2000 { // lo excluded
		t.Fatalf("net: rx=%d tx=%d want 1000/2000", s1.netRx, s1.netTx)
	}
	if len(s1.disks) != 1 || s1.disks[0].Mount != "/" || s1.disks[0].UsedPct != 50 {
		t.Fatalf("disk: %+v", s1.disks)
	}

	s2 := parseSample([]byte(`webmail
Linux
cpu  200 0 100 1700 0 0 0 0 0 0
MemTotal:        8000000 kB
MemAvailable:    4000000 kB
1001.00 900.00
@NET
  eth0: 3000 0 0 0 0 0 0 0 5000 0 0 0 0 0 0 0
@DISK
`))
	m := delta(s1, s2, 1.0)
	if m.CPU < 14.9 || m.CPU > 15.1 { // (1 - 850/1000)*100
		t.Fatalf("cpu%% = %v, want 15", m.CPU)
	}
	if m.NetDown != 2000 || m.NetUp != 3000 {
		t.Fatalf("net rate: down=%d up=%d want 2000/3000", m.NetDown, m.NetUp)
	}
	if m.MemTotal != 8000000*1024 || m.MemUsed != 4000000*1024 {
		t.Fatalf("mem: used=%d total=%d", m.MemUsed, m.MemTotal)
	}
}

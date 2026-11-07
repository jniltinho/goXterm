package gui

import (
	"bufio"
	"bytes"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Remote monitoring (MobaXterm-style bar on SSH tabs): every few seconds a one-shot command reads
// the server's /proc and df, and CPU% / network rates are computed as deltas between samples in Go;
// the result is emitted as "monitor:<tabId>". It runs on an extra exec channel of the SSH session
// (migrate-gui-to-go-wails — remote monitoring; Go-native, core separation).

const monitorEvery = 2 * time.Second

// monCmd prints one sample the parser understands (POSIX sh; works on Linux servers).
const monCmd = `uname -n 2>/dev/null; uname -s 2>/dev/null; head -n1 /proc/stat 2>/dev/null; ` +
	`grep -E '^(MemTotal|MemAvailable):' /proc/meminfo 2>/dev/null; head -n1 /proc/uptime 2>/dev/null; ` +
	`echo @NET; cat /proc/net/dev 2>/dev/null; echo @DISK; df -Pk 2>/dev/null`

// Disk is one filesystem row of the monitored server.
type Disk struct {
	Mount   string `json:"mount"`
	UsedPct int    `json:"usedPct"`
	UsedKiB uint64 `json:"usedKiB"`
	SizeKiB uint64 `json:"sizeKiB"`
}

// Metrics is one emitted monitoring sample (rates already computed). CPU is -1 when unknown.
type Metrics struct {
	Host     string  `json:"host"`
	OS       string  `json:"os"`
	CPU      float64 `json:"cpu"` // percent 0-100
	MemUsed  uint64  `json:"memUsed"`
	MemTotal uint64  `json:"memTotal"`
	NetUp    uint64  `json:"netUp"`   // bytes/s
	NetDown  uint64  `json:"netDown"` // bytes/s
	Uptime   uint64  `json:"uptime"`  // seconds
	Disks    []Disk  `json:"disks"`
}

// sample is one raw reading; CPU and net are cumulative counters turned into rates by delta().
type sample struct {
	host, os                 string
	cpuTotal, cpuIdle        uint64
	memTotalKiB, memAvailKiB uint64
	uptime                   float64
	netRx, netTx             uint64
	disks                    []Disk
	ok                       bool
}

type cmdRunner interface {
	Run(cmd string) ([]byte, error)
}

// MonitorStart begins collecting metrics for an SSH tab; a no-op if already running or not SSH.
func (a *App) MonitorStart(tabID string) error {
	a.mu.Lock()
	if a.mon == nil {
		a.mon = map[string]chan struct{}{}
	}
	if _, running := a.mon[tabID]; running {
		a.mu.Unlock()
		return nil
	}
	runner, ok := a.term[tabID].(cmdRunner)
	if !ok {
		a.mu.Unlock()
		return errors.New("this tab cannot be monitored")
	}
	stop := make(chan struct{})
	a.mon[tabID] = stop
	a.mu.Unlock()
	go a.monitorLoop(tabID, runner, stop)
	return nil
}

// MonitorStop ends a tab's monitoring loop.
func (a *App) MonitorStop(tabID string) {
	a.mu.Lock()
	stop := a.mon[tabID]
	delete(a.mon, tabID)
	a.mu.Unlock()
	if stop != nil {
		close(stop)
	}
}

func (a *App) monitorLoop(tabID string, r cmdRunner, stop chan struct{}) {
	t := time.NewTicker(monitorEvery)
	defer t.Stop()
	var prev sample
	var prevAt time.Time
	for {
		out, err := r.Run(monCmd)
		if err != nil {
			a.emitMon("monitor:err:"+tabID, err.Error())
			return
		}
		cur := parseSample(out)
		now := time.Now()
		if cur.ok && prev.ok {
			a.emitMon("monitor:"+tabID, delta(prev, cur, now.Sub(prevAt).Seconds()))
		}
		if cur.ok {
			prev, prevAt = cur, now
		}
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

func (a *App) emitMon(name string, data any) {
	if a.emit != nil {
		a.emit(name, data)
	}
}

// parseSample reads one monCmd output block.
func parseSample(raw []byte) sample {
	var s sample
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	section := "head"
	line := 0
	for sc.Scan() {
		t := sc.Text()
		switch t {
		case "@NET":
			section = "net"
			continue
		case "@DISK":
			section = "disk"
			continue
		}
		switch section {
		case "head":
			line++
			switch {
			case line == 1:
				s.host = strings.TrimSpace(t)
			case line == 2:
				s.os = strings.TrimSpace(t)
			case strings.HasPrefix(t, "cpu "):
				f := strings.Fields(t)[1:]
				var total uint64
				for i, v := range f {
					n, _ := strconv.ParseUint(v, 10, 64)
					total += n
					if i == 3 { // idle is the 4th field
						s.cpuIdle = n
					}
				}
				s.cpuTotal = total
			case strings.HasPrefix(t, "MemTotal:"):
				s.memTotalKiB = kib(t)
			case strings.HasPrefix(t, "MemAvailable:"):
				s.memAvailKiB = kib(t)
			default:
				if u := strings.Fields(t); len(u) > 0 {
					if v, err := strconv.ParseFloat(u[0], 64); err == nil && s.uptime == 0 {
						s.uptime = v // /proc/uptime first field
					}
				}
			}
		case "net":
			if i := strings.IndexByte(t, ':'); i > 0 {
				iface := strings.TrimSpace(t[:i])
				if iface == "lo" {
					continue
				}
				f := strings.Fields(t[i+1:])
				if len(f) >= 9 {
					rx, _ := strconv.ParseUint(f[0], 10, 64)
					tx, _ := strconv.ParseUint(f[8], 10, 64)
					s.netRx += rx
					s.netTx += tx
				}
			}
		case "disk":
			f := strings.Fields(t)
			if len(f) >= 6 && strings.HasPrefix(f[0], "/") {
				size, _ := strconv.ParseUint(f[1], 10, 64)
				used, _ := strconv.ParseUint(f[2], 10, 64)
				pct, _ := strconv.Atoi(strings.TrimSuffix(f[4], "%"))
				s.disks = append(s.disks, Disk{Mount: f[5], UsedPct: pct, UsedKiB: used, SizeKiB: size})
			}
		}
	}
	s.ok = s.host != "" || s.cpuTotal != 0 || s.memTotalKiB != 0
	return s
}

func kib(line string) uint64 {
	f := strings.Fields(line)
	if len(f) >= 2 {
		n, _ := strconv.ParseUint(f[1], 10, 64)
		return n
	}
	return 0
}

// delta turns two cumulative samples into rates and the display metrics.
func delta(prev, cur sample, dt float64) Metrics {
	m := Metrics{Host: cur.host, OS: cur.os, Uptime: uint64(cur.uptime), Disks: cur.disks, CPU: -1}
	if cur.memTotalKiB > 0 {
		m.MemTotal = cur.memTotalKiB * 1024
		used := cur.memTotalKiB
		if cur.memAvailKiB <= cur.memTotalKiB {
			used = cur.memTotalKiB - cur.memAvailKiB
		}
		m.MemUsed = used * 1024
	}
	if dt > 0 {
		if cur.cpuTotal > prev.cpuTotal {
			dTotal := cur.cpuTotal - prev.cpuTotal
			dIdle := uint64(0)
			if cur.cpuIdle > prev.cpuIdle {
				dIdle = cur.cpuIdle - prev.cpuIdle
			}
			m.CPU = (1 - float64(dIdle)/float64(dTotal)) * 100
		}
		if cur.netRx >= prev.netRx {
			m.NetDown = uint64(float64(cur.netRx-prev.netRx) / dt)
		}
		if cur.netTx >= prev.netTx {
			m.NetUp = uint64(float64(cur.netTx-prev.netTx) / dt)
		}
	}
	return m
}

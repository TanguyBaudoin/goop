package ui

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

const barWidth = 24

// RenderBar formats one progress line: a filled/empty block bar,
// percent, human-readable byte counts, and transfer rate. total <= 0
// means the size is unknown (no Content-Length header) -- shown as a
// running byte count with no bar/percentage rather than a fake one.
func RenderBar(label string, downloaded, total int64, rate float64) string {
	label = truncate(label, 22)
	if total <= 0 {
		return fmt.Sprintf("  %s %-22s %9s          %s/s",
			Cyan(Arrow), label, HumanBytes(downloaded), HumanBytes(int64(rate)))
	}
	pct := float64(downloaded) / float64(total)
	if pct > 1 {
		pct = 1
	}
	filled := int(pct * barWidth)
	bar := Green(strings.Repeat("█", filled)) + Dim(strings.Repeat("░", barWidth-filled))
	return fmt.Sprintf("  %s %-22s %s  %3.0f%%  %s/%s  %s/s",
		Cyan(Arrow), label, bar, pct*100, HumanBytes(downloaded), HumanBytes(total), HumanBytes(int64(rate)))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// HumanBytes formats n as a human-readable size (B/KB/MB/GB/TB).
func HumanBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGT"[exp])
}

// Progress renders one live, in-place line per active download (A1: the
// installer's worker pool runs several at once), redrawn via ANSI cursor
// movement so bars update without scrolling the terminal. Println shares
// the same lock and always prints above the bars, erasing and redrawing
// them around the line so ordinary log output and the bars never tear or
// overwrite each other.
//
// Falls back to plain scrolling output (no erase/redraw) when Enabled is
// false -- output redirected, NO_COLOR, etc. -- since cursor-movement
// tricks only make sense on a real terminal, and Update still logs
// nothing extra in that case rather than spamming a redirected log with
// dozens of intermediate frames.
type Progress struct {
	mu    sync.Mutex
	order []string
	lines map[string]string
	drawn int
}

func NewProgress() *Progress {
	return &Progress{lines: make(map[string]string)}
}

// Println prints s above the active bars, keeping them pinned at the
// bottom.
func (p *Progress) Println(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.eraseLocked()
	fmt.Fprintln(os.Stderr, s)
	p.drawLocked()
}

// Update sets (or creates, on first call for id) the bar text for id and
// redraws.
func (p *Progress) Update(id, line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.lines[id]; !ok {
		p.order = append(p.order, id)
	}
	p.lines[id] = line
	p.eraseLocked()
	p.drawLocked()
}

// Done removes id's bar -- the download finished or failed, and whatever
// outcome line the installer logs (via Println) will report it.
func (p *Progress) Done(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.eraseLocked()
	delete(p.lines, id)
	for i, x := range p.order {
		if x == id {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}
	p.drawLocked()
}

func (p *Progress) eraseLocked() {
	if !Enabled || p.drawn == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "\x1b[%dA", p.drawn)
	for i := 0; i < p.drawn; i++ {
		fmt.Fprint(os.Stderr, "\x1b[2K\n")
	}
	fmt.Fprintf(os.Stderr, "\x1b[%dA", p.drawn)
	p.drawn = 0
}

func (p *Progress) drawLocked() {
	if !Enabled {
		return
	}
	for _, id := range p.order {
		fmt.Fprintln(os.Stderr, p.lines[id])
	}
	p.drawn = len(p.order)
}

package logbuf

import (
	"strings"
	"sync"
	"time"
)

type Entry struct {
	Time time.Time `json:"time"`
	Text string    `json:"text"`
}

type Buffer struct {
	mu      sync.Mutex
	entries []Entry
	max     int
	persist *DailyFile
}

func New(max int) *Buffer {
	return &Buffer{max: max}
}

func (b *Buffer) SetPersist(d *DailyFile) {
	b.mu.Lock()
	b.persist = d
	b.mu.Unlock()
}

func (b *Buffer) Write(p []byte) (int, error) {
	now := time.Now()
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line == "" {
			continue
		}
		b.mu.Lock()
		b.entries = append(b.entries, Entry{Time: now, Text: line})
		if len(b.entries) > b.max*2 {
			b.entries = append([]Entry{}, b.entries[len(b.entries)-b.max:]...)
		}
		persist := b.persist
		b.mu.Unlock()
		if persist != nil {
			_ = persist.Append(now, line)
		}
	}
	return len(p), nil
}

func (b *Buffer) Dates() ([]string, error) {
	b.mu.Lock()
	p := b.persist
	b.mu.Unlock()
	if p == nil {
		return nil, nil
	}
	return Dates(p.dir, p.prefix)
}

func (b *Buffer) ReadDate(date string) ([]Entry, error) {
	b.mu.Lock()
	p := b.persist
	b.mu.Unlock()
	if p == nil {
		return nil, nil
	}
	return ReadDate(p.dir, p.prefix, date)
}

func (b *Buffer) Recent(n int) []Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 || n > len(b.entries) {
		n = len(b.entries)
	}
	out := make([]Entry, n)
	copy(out, b.entries[len(b.entries)-n:])
	return out
}

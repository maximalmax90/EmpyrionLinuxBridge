package logbuf

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type DailyFile struct {
	mu      sync.Mutex
	dir     string
	prefix  string
	curDate string
	f       *os.File
}

func NewDailyFile(dir, prefix string) *DailyFile {
	return &DailyFile{dir: dir, prefix: prefix}
}

func (d *DailyFile) Append(t time.Time, text string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	day := t.Format("2006-01-02")
	if d.f == nil || day != d.curDate {
		if d.f != nil {
			d.f.Close()
			d.f = nil
		}
		if err := os.MkdirAll(d.dir, 0o777); err != nil {
			return fmt.Errorf("dailylog: mkdir %s: %w", d.dir, err)
		}
		f, err := os.OpenFile(filepath.Join(d.dir, d.prefix+"_"+day+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
		if err != nil {
			return fmt.Errorf("dailylog: open: %w", err)
		}
		d.f = f
		d.curDate = day
	}
	_, err := fmt.Fprintf(d.f, "%s %s\n", t.Format("2006/01/02 15:04:05"), text)
	return err
}

var dailyFileNameRe = regexp.MustCompile(`_(\d{4}-\d{2}-\d{2})\.log$`)

func Dates(dir, prefix string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var dates []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix+"_") {
			continue
		}
		if m := dailyFileNameRe.FindStringSubmatch(e.Name()); m != nil {
			dates = append(dates, m[1])
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	return dates, nil
}

var dailyLineRe = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}) (.*)$`)

func ReadDate(dir, prefix, date string) ([]Entry, error) {
	data, err := os.ReadFile(filepath.Join(dir, prefix+"_"+date+".log"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Entry
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		if m := dailyLineRe.FindStringSubmatch(line); m != nil {
			if t, terr := time.ParseInLocation("2006/01/02 15:04:05", m[1], time.Local); terr == nil {
				out = append(out, Entry{Time: t, Text: m[2]})
				continue
			}
		}
		out = append(out, Entry{Text: line})
	}
	return out, sc.Err()
}

package scheduler

import (
	"testing"
	"time"
)

func TestParseTimeOfDay(t *testing.T) {
	cases := []struct {
		in                  string
		wantH, wantM, wantS int
		wantOK              bool
	}{
		{"14:05", 14, 5, 0, true},
		{"00:00", 0, 0, 0, true},
		{"23:59", 23, 59, 0, true},
		{"14:05:30", 14, 5, 30, true},
		{"00:00:00", 0, 0, 0, true},
		{"23:59:59", 23, 59, 59, true},
		{"24:00", 0, 0, 0, false},
		{"14:60", 0, 0, 0, false},
		{"14:05:60", 0, 0, 0, false},
		{"", 0, 0, 0, false},
		{"garbage", 0, 0, 0, false},
	}
	for _, c := range cases {
		h, m, s, ok := parseTimeOfDay(c.in)
		if ok != c.wantOK {
			t.Errorf("parseTimeOfDay(%q) ok = %v, want %v", c.in, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if h != c.wantH || m != c.wantM || s != c.wantS {
			t.Errorf("parseTimeOfDay(%q) = %d:%d:%d, want %d:%d:%d", c.in, h, m, s, c.wantH, c.wantM, c.wantS)
		}
	}
}

func TestNextWeekdayAt(t *testing.T) {
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

	got := nextWeekdayAt(now, time.Monday, "08:00")
	want := time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("время уже прошло: got %v, want %v", got, want)
	}

	got = nextWeekdayAt(now, time.Monday, "18:00")
	want = time.Date(2026, 9, 21, 18, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("время ещё не наступило: got %v, want %v", got, want)
	}
}

func TestNextMonthDayAt(t *testing.T) {
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

	got := nextMonthDayAt(now, 5, "08:00")
	want := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("день уже прошёл: got %v, want %v", got, want)
	}
}

func TestNextMonthDayAt_ClampsShortMonths(t *testing.T) {
	now := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	got := nextMonthDayAt(now, 31, "12:00")
	want := time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("клэмп короткого месяца: got %v, want %v", got, want)
	}
}

func TestNextIntervalOnWeekday(t *testing.T) {

	now := time.Date(2026, 9, 18, 14, 0, 0, 0, time.UTC)
	got := nextIntervalOnWeekday(now, 30, time.Friday)
	want := time.Date(2026, 9, 18, 14, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("тот же день, +N не выходит за полночь: got %v, want %v", got, want)
	}

	now = time.Date(2026, 9, 18, 23, 50, 0, 0, time.UTC)
	got = nextIntervalOnWeekday(now, 30, time.Friday)
	want = time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("переход через полночь на неподходящий день: got %v, want %v", got, want)
	}

	now = time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC)
	got = nextIntervalOnWeekday(now, 15, time.Friday)
	want = time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("now не в подходящем дне: got %v, want %v", got, want)
	}
}

func TestNextIntervalOnMonthDay(t *testing.T) {

	now := time.Date(2026, 9, 15, 14, 0, 0, 0, time.UTC)
	got := nextIntervalOnMonthDay(now, 30, 15)
	want := time.Date(2026, 9, 15, 14, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("тот же день, +N не выходит за полночь: got %v, want %v", got, want)
	}

	now = time.Date(2026, 9, 15, 23, 50, 0, 0, time.UTC)
	got = nextIntervalOnMonthDay(now, 30, 15)
	want = time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("переход через полночь на неподходящий день: got %v, want %v", got, want)
	}

	now = time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	got = nextIntervalOnMonthDay(now, 15, 15)
	want = time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("now не в подходящем дне: got %v, want %v", got, want)
	}

	now = time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)
	got = nextIntervalOnMonthDay(now, 60, 31)
	want = time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("клэмп: ближайшее 31-е — конец января: got %v, want %v", got, want)
	}
}

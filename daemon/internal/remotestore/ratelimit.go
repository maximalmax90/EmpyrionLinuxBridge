package remotestore

import (
	"io"
	"time"
)

type throttledReader struct {
	r           io.Reader
	bytesPerSec int64
	start       time.Time
	sent        int64
}

func newThrottledReader(r io.Reader, bytesPerSec int64) io.Reader {
	if bytesPerSec <= 0 {
		return r
	}
	return &throttledReader{r: r, bytesPerSec: bytesPerSec, start: time.Now()}
}

func (t *throttledReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		t.sent += int64(n)
		wantElapsed := time.Duration(float64(t.sent) / float64(t.bytesPerSec) * float64(time.Second))
		if actualElapsed := time.Since(t.start); wantElapsed > actualElapsed {
			time.Sleep(wantElapsed - actualElapsed)
		}
	}
	return n, err
}

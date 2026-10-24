package modclient

import (
	"fmt"
	"sync"
	"time"
)

const AllStructuresMaxAge = 30 * time.Second

var allStructures struct {
	mu        sync.Mutex
	list      []any
	fetchedAt time.Time
}

func (c *Client) AllStructures(maxAge time.Duration) ([]any, error) {
	allStructures.mu.Lock()
	defer allStructures.mu.Unlock()
	if maxAge > 0 && allStructures.list != nil && time.Since(allStructures.fetchedAt) < maxAge {
		return allStructures.list, nil
	}
	v, err := c.GetSlow("/structures/all")
	if err != nil {
		return nil, err
	}
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("mod GET /structures/all: unexpected response: %T", v)
	}
	allStructures.list = list
	allStructures.fetchedAt = time.Now()
	return list, nil
}

func ResetAllStructuresCache() {
	allStructures.mu.Lock()
	allStructures.list = nil
	allStructures.fetchedAt = time.Time{}
	allStructures.mu.Unlock()
}

package bundle

import (
	"bytes"
	"compress/gzip"
	"container/list"
	"crypto/sha256"
	"fmt"
	"io"
	"sync"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

const snapshotLimit = 512 * 1024 * 1024

// Only immutable decompressed bytes are shared. Every caller still parses and
// validates its own Bundle. Failed decompression is never retained.
var snapshots = newSnapshotCache(snapshotLimit)

type snapshotEntry struct {
	key string
	raw []byte
}
type snapshotFlight struct {
	done chan struct{}
	raw  []byte
	err  error
}
type snapshotCache struct {
	mu             sync.Mutex
	capacity, used int
	entries        map[string]*list.Element
	order          *list.List
	flights        map[string]*snapshotFlight
	decompressions int
}

func newSnapshotCache(capacity int) *snapshotCache {
	return &snapshotCache{capacity: capacity, entries: map[string]*list.Element{}, order: list.New(), flights: map[string]*snapshotFlight{}}
}
func (c *snapshotCache) load(profile string, compressed []byte) ([]byte, error) {
	key := fmt.Sprintf("%s:%x", profile, sha256.Sum256(compressed))
	c.mu.Lock()
	if e := c.entries[key]; e != nil {
		c.order.MoveToFront(e)
		raw := e.Value.(snapshotEntry).raw
		c.mu.Unlock()
		return raw, nil
	}
	if f := c.flights[key]; f != nil {
		c.mu.Unlock()
		<-f.done
		return f.raw, f.err
	}
	f := &snapshotFlight{done: make(chan struct{})}
	c.flights[key] = f
	c.decompressions++
	c.mu.Unlock()
	raw, err := decompressSnapshot(compressed)
	c.mu.Lock()
	if err == nil && len(raw) <= c.capacity {
		for c.used+len(raw) > c.capacity {
			e := c.order.Back()
			v := e.Value.(snapshotEntry)
			delete(c.entries, v.key)
			c.used -= len(v.raw)
			c.order.Remove(e)
		}
		c.entries[key] = c.order.PushFront(snapshotEntry{key, raw})
		c.used += len(raw)
	}
	f.raw, f.err = raw, err
	delete(c.flights, key)
	close(f.done)
	c.mu.Unlock()
	return raw, err
}
func decompressSnapshot(compressed []byte) ([]byte, error) {
	return decompressSnapshotWithLimit(compressed, snapshotLimit)
}

func decompressSnapshotWithLimit(compressed []byte, limit int) ([]byte, error) {
	z, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer z.Close()
	raw, err := io.ReadAll(io.LimitReader(z, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, domain.Fail("BUNDLE", "快照超过限制")
	}
	return raw, nil
}

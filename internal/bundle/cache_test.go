package bundle

import (
	"bytes"
	"compress/gzip"
	"sync"
	"testing"
)

func compressed(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	if _, e := z.Write([]byte(s)); e != nil {
		t.Fatal(e)
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestSnapshotCacheConcurrentIsolationAndEviction(t *testing.T) {
	c := newSnapshotCache(12)
	a := compressed(t, "aaaaaaaa")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, e := c.load("spec", a)
			if e != nil || string(b) != "aaaaaaaa" {
				t.Errorf("load: %q %v", b, e)
			}
		}()
	}
	wg.Wait()
	if c.decompressions != 1 {
		t.Fatalf("duplicate decompression: %d", c.decompressions)
	}
	if _, e := c.load("backend", compressed(t, "bbbbbbbb")); e != nil {
		t.Fatal(e)
	}
	if c.used > 12 || len(c.entries) != 1 {
		t.Fatal("capacity not bounded")
	}
	if _, e := c.load("spec", a); e != nil {
		t.Fatal(e)
	}
	if c.decompressions != 3 {
		t.Fatal("evicted bytes were reused")
	}
	if _, e := c.load("spec", []byte("corrupt")); e == nil {
		t.Fatal("corrupt gzip accepted")
	}
	if len(c.entries) != 1 {
		t.Fatal("failure cached")
	}
}
func TestLoadReturnsIndependentValidatedObjects(t *testing.T) {
	a, e := Load("spec")
	if e != nil {
		t.Fatal(e)
	}
	original := a.TemplateCommit
	a.TemplateCommit = "mutated"
	a.Files["invented"] = File{}
	a.Manifest["mutated"] = true
	b, e := Load("spec")
	if e != nil {
		t.Fatal(e)
	}
	if b.TemplateCommit != original || b.Manifest["mutated"] != nil {
		t.Fatal("shared mutable bundle")
	}
	if _, ok := b.Files["invented"]; ok {
		t.Fatal("shared files")
	}
}

func TestSnapshotCacheUsesContentAndProfileIdentityAndLRU(t *testing.T) {
	c := newSnapshotCache(12)
	a := compressed(t, "aaaa")
	b := compressed(t, "bbbb")
	d := compressed(t, "dddd")
	for _, x := range []struct {
		profile string
		raw     []byte
	}{{"spec", a}, {"backend", b}, {"frontend", d}, {"spec", a}, {"design", compressed(t, "eeee")}} {
		if _, e := c.load(x.profile, x.raw); e != nil {
			t.Fatal(e)
		}
	}
	before := c.decompressions
	if _, e := c.load("spec", a); e != nil {
		t.Fatal(e)
	}
	if c.decompressions != before {
		t.Fatal("recently used entry evicted")
	}
	if _, e := c.load("backend", b); e != nil {
		t.Fatal(e)
	}
	if c.decompressions != before+1 {
		t.Fatal("oldest entry not evicted")
	}
	c = newSnapshotCache(1)
	for i := 0; i < 2; i++ {
		if _, e := c.load("spec", a); e != nil {
			t.Fatal(e)
		}
	}
	if c.used != 0 || c.decompressions != 2 {
		t.Fatal("oversized entry retained")
	}
	c = newSnapshotCache(12)
	if _, e := c.load("spec", a); e != nil {
		t.Fatal(e)
	}
	if _, e := c.load("spec", b); e != nil {
		t.Fatal(e)
	}
	if _, e := c.load("backend", b); e != nil {
		t.Fatal(e)
	}
	if c.decompressions != 3 {
		t.Fatal("content or profile identity conflated")
	}
}

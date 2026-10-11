package bundle

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"sync"
	"testing"
)

func TestLoadWarmAllocationBudget(t *testing.T) {
	if _, err := Load("spec"); err != nil {
		t.Fatal(err)
	}
	allocations := testing.AllocsPerRun(3, func() {
		if _, err := Load("spec"); err != nil {
			t.Fatal(err)
		}
	})
	// A warm read should copy its mutable view, not decode and validate the
	// entire 30 MB archive again (the regression allocated >175,000 objects).
	if allocations > 50_000 {
		t.Fatalf("warm Bundle allocation budget exceeded: %.0f", allocations)
	}
}

func TestLoadNestedMutationDoesNotChangeLaterReaders(t *testing.T) {
	a, err := Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var mutate func(any)
	mutate = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			for _, child := range v {
				mutate(child)
			}
			v["caller-mutation"] = true
		case []any:
			for _, child := range v {
				mutate(child)
			}
			if len(v) > 0 {
				v[0] = "caller-mutation"
			}
		}
	}
	mutate(a.Manifest)
	mutate(a.Distribution)
	for key := range a.Initial {
		delete(a.Initial, key)
		break
	}
	for _, group := range []map[string]Requirement{a.StageRequirements, a.SkillRequirements} {
		for _, req := range group {
			if len(req.Paths) > 0 {
				req.Paths[0] = "caller-mutation"
			}
			if len(req.Skills) > 0 {
				req.Skills[0] = "caller-mutation"
			}
		}
	}
	if a.Upgrade != nil {
		for i := range a.Upgrade.Assets {
			if len(a.Upgrade.Assets[i].Prefixes) > 0 {
				a.Upgrade.Assets[i].Prefixes[0] = "caller-mutation"
			}
			if len(a.Upgrade.Assets[i].Paths) > 0 {
				a.Upgrade.Assets[i].Paths[0] = "caller-mutation"
			}
		}
		a.Upgrade.Rules = append(a.Upgrade.Rules, MigrationRule{ID: "caller-mutation"})
	}
	if len(a.NativeTransforms) > 0 {
		a.NativeTransforms[0].Source.Data = "caller-mutation"
	}
	b, err := Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(b)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("nested caller mutation reached later reader", err)
	}
}

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

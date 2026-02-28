package copier

import (
	"sync"
	"testing"
)

func TestLazyMap_Get(t *testing.T) {
	calls := 0
	m := NewLazyMap[int](map[string]func() int{
		"a": func() int { calls++; return 42 },
		"b": func() int { return 7 },
	})

	v, ok := m.Get("a")
	if !ok || v != 42 {
		t.Fatalf("expected (42, true), got (%d, %v)", v, ok)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}

	// Second access should use cache.
	v, ok = m.Get("a")
	if !ok || v != 42 {
		t.Fatalf("expected (42, true) from cache, got (%d, %v)", v, ok)
	}
	if calls != 1 {
		t.Fatalf("expected still 1 call, got %d", calls)
	}

	// Missing key.
	_, ok = m.Get("missing")
	if ok {
		t.Fatal("expected false for missing key")
	}
}

func TestLazyMap_Set(t *testing.T) {
	m := NewLazyMap[string](map[string]func() string{
		"x": func() string { return "computed" },
	})
	m.Set("x", "preset")
	v, ok := m.Get("x")
	if !ok || v != "preset" {
		t.Fatalf("expected preset value, got %q", v)
	}
}

func TestLazyMap_ConcurrentAccess(t *testing.T) {
	m := NewLazyMap[int](map[string]func() int{
		"counter": func() int { return 1 },
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Get("counter")
		}()
	}
	wg.Wait()

	v, _ := m.Get("counter")
	if v != 1 {
		t.Fatalf("expected 1, got %d", v)
	}
}

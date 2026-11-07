package runtimecore

import (
	"errors"
	"fmt"
	"testing"
)

func TestMaterializerResolveCachesByVersion(t *testing.T) {
	m := NewMaterializer[*int]()
	buildCalls := 0

	first, err := m.Resolve("route-1", "v1", func() (*int, error) {
		buildCalls++
		value := 1
		return &value, nil
	})
	if err != nil {
		t.Fatalf("first Resolve() error = %v", err)
	}

	second, err := m.Resolve("route-1", "v1", func() (*int, error) {
		buildCalls++
		value := 2
		return &value, nil
	})
	if err != nil {
		t.Fatalf("second Resolve() error = %v", err)
	}

	if first != second {
		t.Fatalf("cached instance mismatch: first=%p second=%p", first, second)
	}
	if buildCalls != 1 {
		t.Fatalf("build calls = %d, want 1", buildCalls)
	}
	if first == nil || *first != 1 {
		t.Fatalf("first value = %#v, want 1", first)
	}
}

func TestMaterializerResolveRebuildsWhenVersionChanges(t *testing.T) {
	m := NewMaterializer[*int]()
	buildCalls := 0

	first, err := m.Resolve("route-1", "v1", func() (*int, error) {
		buildCalls++
		value := 1
		return &value, nil
	})
	if err != nil {
		t.Fatalf("first Resolve() error = %v", err)
	}

	second, err := m.Resolve("route-1", "v2", func() (*int, error) {
		buildCalls++
		value := 2
		return &value, nil
	})
	if err != nil {
		t.Fatalf("second Resolve() error = %v", err)
	}

	if first == second {
		t.Fatal("Resolve() reused cached instance after version change")
	}
	if buildCalls != 2 {
		t.Fatalf("build calls = %d, want 2", buildCalls)
	}
	if second == nil || *second != 2 {
		t.Fatalf("second value = %#v, want 2", second)
	}
}

func TestMaterializerInvalidateForcesRebuild(t *testing.T) {
	m := NewMaterializer[*int]()
	buildCalls := 0

	_, err := m.Resolve("route-1", "v1", func() (*int, error) {
		buildCalls++
		value := 1
		return &value, nil
	})
	if err != nil {
		t.Fatalf("first Resolve() error = %v", err)
	}

	m.Invalidate("route-1")

	_, err = m.Resolve("route-1", "v1", func() (*int, error) {
		buildCalls++
		value := 2
		return &value, nil
	})
	if err != nil {
		t.Fatalf("second Resolve() error = %v", err)
	}

	if buildCalls != 2 {
		t.Fatalf("build calls = %d, want 2", buildCalls)
	}
}

func TestMaterializerResolveDoesNotCacheErrors(t *testing.T) {
	m := NewMaterializer[*int]()
	buildCalls := 0
	wantErr := errors.New("boom")

	_, err := m.Resolve("route-1", "v1", func() (*int, error) {
		buildCalls++
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("first Resolve() error = %v, want %v", err, wantErr)
	}

	value, err := m.Resolve("route-1", "v1", func() (*int, error) {
		buildCalls++
		v := 2
		return &v, nil
	})
	if err != nil {
		t.Fatalf("second Resolve() error = %v", err)
	}

	if buildCalls != 2 {
		t.Fatalf("build calls = %d, want 2", buildCalls)
	}
	if value == nil || *value != 2 {
		t.Fatalf("value = %#v, want 2", value)
	}
}

func TestMaterializerLateBuildCannotUndoEviction(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(fmt.Sprint(reset), func(t *testing.T) {
			cache := NewMaterializer[string]()
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			go func() {
				defer close(done)
				_, _ = cache.Resolve("route", "old", func() (string, error) { close(entered); <-release; return "old", nil })
			}()
			<-entered
			if reset {
				cache.Reset()
			} else {
				cache.Invalidate("route")
			}
			current, err := cache.Resolve("route", "new", func() (string, error) { return "new", nil })
			if err != nil || current != "new" {
				t.Fatal("current build failed")
			}
			close(release)
			<-done
			rebuilt := false
			current, err = cache.Resolve("route", "new", func() (string, error) { rebuilt = true; return "unexpected", nil })
			if err != nil || current != "new" || rebuilt {
				t.Fatal("late retired build overwrote current cache")
			}
		})
	}
}

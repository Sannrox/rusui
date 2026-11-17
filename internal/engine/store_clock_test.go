package engine

import (
	"testing"
	"time"
)

// Two instances whose local clocks disagree by minutes read the same time
// once anchored to their shared store (ADR 0089 D4).
func TestStoreClockAlignsSkewedInstances(t *testing.T) {
	stores := OpenSharedTestStores(t, 2)
	ahead, behind := NewStoreClock(stores[0]), NewStoreClock(stores[1])
	ahead.local = func() time.Time { return time.Now().Add(5 * time.Minute) }
	behind.local = func() time.Time { return time.Now().Add(-5 * time.Minute) }
	for _, c := range []*StoreClock{ahead, behind} {
		if err := c.Sync(); err != nil {
			t.Fatal(err)
		}
	}
	if d := ahead.Now().Sub(behind.Now()).Abs(); d > time.Second {
		t.Fatalf("anchored clocks differ by %v", d)
	}
	if d := time.Until(ahead.Now()).Abs(); d > time.Second {
		t.Fatalf("anchored clock is %v from the store's", d)
	}
}

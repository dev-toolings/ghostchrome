package engine

import (
	"sync"
	"testing"
)

func TestDialogAutoPolicyConcurrent(t *testing.T) {
	policy := &DialogAutoPolicy{}
	policy.Set(true, "accept")

	const iterations = 10_000
	var wg sync.WaitGroup
	for writer := 0; writer < 4; writer++ {
		writer := writer
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if (i+writer)%2 == 0 {
					policy.Set(true, "accept")
				} else {
					policy.Set(false, "dismiss")
				}
			}
		}()
	}
	for reader := 0; reader < 4; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				accept, prompt := policy.Snapshot()
				if (accept && prompt != "accept") || (!accept && prompt != "dismiss") {
					t.Errorf("inconsistent policy snapshot: accept=%t prompt=%q", accept, prompt)
					return
				}
			}
		}()
	}
	wg.Wait()
}

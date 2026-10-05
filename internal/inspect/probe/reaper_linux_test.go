//go:build linux

package probe

import (
	"context"
	"os"
	"testing"
	"time"
)

// After a probe that left a setsid-escaped descendant behind, no live
// process may remain adopted by WOMM.
func TestNoAdoptedChildrenRemainAfterRun(t *testing.T) {
	if os.Getpid() == 1 {
		t.Skip("pid 1 skips the sweep by design")
	}
	p := script(t, "setsid sleep 30 >/dev/null 2>&1 &\necho 1.0.0\n")
	if _, err := Run(context.Background(), spec(p, 0)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if left := childrenOf(os.Getpid()); len(left) != 0 {
		t.Fatalf("live adopted children remain: %v", left)
	}
}

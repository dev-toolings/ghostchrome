package ops_test

import (
	"testing"

	"github.com/dev-toolings/ghostchrome/internal/ops"
)

// TestCatalogValid runs the same self-consistency check the generator runs
// before it writes a single line. It is the one guard that has to stay a test:
// the generator only runs on demand, this runs on every `go test`.
func TestCatalogValid(t *testing.T) {
	for _, err := range ops.Validate() {
		t.Error(err)
	}
}

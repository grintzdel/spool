package system_test

import (
	"testing"

	"github.com/grintzdel/spool/internal/adapter/system"
)

func TestULIDsAreUniqueAndSortedInCreationOrder(t *testing.T) {
	ids := system.NewULIDs()
	prev := ""
	for range 10_000 {
		id := ids.NewID()
		if len(id) != 26 || id <= prev {
			t.Fatalf("id %q after %q", id, prev)
		}
		prev = id
	}
}

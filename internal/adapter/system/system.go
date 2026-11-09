package system

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

type Clock struct{}

func (Clock) Now() time.Time { return time.Now() }

type ULIDs struct {
	mu      sync.Mutex
	entropy *ulid.MonotonicEntropy
}

func NewULIDs() *ULIDs {
	return &ULIDs{entropy: ulid.Monotonic(rand.Reader, 0)}
}

func (u *ULIDs) NewID() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return ulid.MustNew(ulid.Now(), u.entropy).String()
}

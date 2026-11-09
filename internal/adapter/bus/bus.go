package bus

import (
	"sync"

	"github.com/grintzdel/spool/internal/usecase"
)

type Bus struct {
	mu     sync.RWMutex
	subs   map[chan usecase.Event]struct{}
	buffer int
}

func New(buffer int) *Bus {
	return &Bus{subs: make(map[chan usecase.Event]struct{}), buffer: buffer}
}

func (b *Bus) Publish(e usecase.Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (b *Bus) Subscribe() (<-chan usecase.Event, func()) {
	ch := make(chan usecase.Event, b.buffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, ch)
			close(ch)
			b.mu.Unlock()
		})
	}
}

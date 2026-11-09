package bus_test

import (
	"testing"
	"time"

	"github.com/grintzdel/spool/internal/adapter/bus"
	"github.com/grintzdel/spool/internal/usecase"
)

func TestSubscribersReceivePublishedEvents(t *testing.T) {
	b := bus.New(4)
	a, unsubA := b.Subscribe()
	c, unsubC := b.Subscribe()
	defer unsubC()

	b.Publish(usecase.Event{Kind: usecase.EventRequest, RequestID: "1"})

	for _, ch := range []<-chan usecase.Event{a, c} {
		select {
		case e := <-ch:
			if e.RequestID != "1" {
				t.Fatalf("event %+v", e)
			}
		case <-time.After(time.Second):
			t.Fatal("event not delivered")
		}
	}

	unsubA()
	if _, open := <-a; open {
		t.Fatal("channel should be closed after unsubscribe")
	}
	unsubA()
}

func TestSlowSubscriberNeverBlocksPublisher(t *testing.T) {
	b := bus.New(1)
	_, unsub := b.Subscribe()
	defer unsub()

	done := make(chan struct{})
	go func() {
		for range 1000 {
			b.Publish(usecase.Event{Kind: usecase.EventAttempt})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publish blocked on a slow subscriber")
	}
}

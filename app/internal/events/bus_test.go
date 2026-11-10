package events

import (
	"reflect"
	"sync"
	"testing"
)

func TestBusPublishesToSubscribersInRegistrationOrder(t *testing.T) {
	bus := NewBus()
	var calls []string
	bus.Subscribe(func(event Event) { calls = append(calls, "first:"+event.Content) })
	bus.Subscribe(func(event Event) { calls = append(calls, "second:"+event.Content) })

	bus.Publish(Event{Content: "hello"})
	want := []string{"first:hello", "second:hello"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v want=%v", calls, want)
	}
}

func TestBusRecoversHandlerPanicAndContinuesDelivery(t *testing.T) {
	bus := NewBus()
	called := false
	bus.Subscribe(func(Event) { panic("broken subscriber") })
	bus.Subscribe(func(event Event) {
		called = event.Content == "still delivered"
	})

	bus.Publish(Event{Content: "still delivered"})
	if !called {
		t.Fatal("a handler panic prevented delivery to the next subscriber")
	}
}

func TestBusUnsubscribeIsIdempotent(t *testing.T) {
	bus := NewBus()
	calls := 0
	unsubscribe := bus.Subscribe(func(Event) { calls++ })
	unsubscribe()
	unsubscribe()
	bus.Publish(Event{})
	if calls != 0 {
		t.Fatalf("unsubscribed handler called %d times", calls)
	}
}

func TestBusSubscribersReceiveIndependentEventCopies(t *testing.T) {
	bus := NewBus()
	bus.Subscribe(func(event Event) {
		event.Emoticons[0].Text = "mutated"
	})
	var received Event
	bus.Subscribe(func(event Event) { received = event })

	bus.Publish(Event{Emoticons: []EmoticonToken{{Text: "original"}}})
	if received.Emoticons[0].Text != "original" {
		t.Fatalf("subscriber mutation leaked into another subscriber: %+v", received.Emoticons)
	}
}

func TestBufferPublishesLiveAndSimulationEventsThroughSameBus(t *testing.T) {
	bus := NewBus()
	buffer := NewBuffer(bus)
	var received []Event
	bus.Subscribe(func(event Event) { received = append(received, event) })

	live := buffer.Add(Event{PlatformID: "bilibili", Type: "danmaku", Content: "live"})
	simulated, err := buffer.AddSimulationEvent("bilibili", SimulationEventInput{
		Type: "danmaku", Content: "simulation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(received) != 2 {
		t.Fatalf("received %d events, want 2", len(received))
	}
	if received[0].ID != live.ID || received[0].Source != "live" {
		t.Fatalf("unexpected live event delivery: %+v", received[0])
	}
	if received[1].ID != simulated.ID || received[1].Source != "simulation" || !received[1].IsSimulation {
		t.Fatalf("unexpected simulation event delivery: %+v", received[1])
	}
}

func TestBusSerializesConcurrentPublishCalls(t *testing.T) {
	bus := NewBus()
	var mu sync.Mutex
	var received []int
	bus.Subscribe(func(event Event) {
		mu.Lock()
		received = append(received, event.UserID)
		mu.Unlock()
	})

	const count = 64
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			bus.Publish(Event{UserID: id})
		}(i)
	}
	wg.Wait()
	if len(received) != count {
		t.Fatalf("received %d events, want %d", len(received), count)
	}
	seen := make(map[int]bool, count)
	for _, id := range received {
		if seen[id] {
			t.Fatalf("duplicate event %d", id)
		}
		seen[id] = true
	}
}

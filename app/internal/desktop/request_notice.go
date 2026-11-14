package desktop

import (
	"encoding/json"
	"sync"
	"time"
)

type RequestNotice struct {
	ID uint64 `json:"id"`
	Kind string `json:"kind"`
	UserName string `json:"user_name"`
	ItemName string `json:"item_name,omitempty"`
	Reason string `json:"reason,omitempty"`
	Timestamp int64 `json:"timestamp"`
}

type RequestNoticeBroker struct {
	mu sync.Mutex
	nextID uint64
	subscribers map[uint64]chan RequestNotice
	nextSubscriber uint64
}

func NewRequestNoticeBroker() *RequestNoticeBroker {
	return &RequestNoticeBroker{subscribers: make(map[uint64]chan RequestNotice)}
}

func (b *RequestNoticeBroker) Publish(notice RequestNotice) {
	if b == nil { return }
	b.mu.Lock()
	b.nextID++
	notice.ID = b.nextID
	notice.Timestamp = time.Now().UnixMilli()
	for _, ch := range b.subscribers {
		select { case ch <- notice: default: }
	}
	b.mu.Unlock()
}

func (b *RequestNoticeBroker) Subscribe() (<-chan RequestNotice, func()) {
	ch := make(chan RequestNotice, 32)
	if b == nil { close(ch); return ch, func(){} }
	b.mu.Lock()
	b.nextSubscriber++
	id := b.nextSubscriber
	b.subscribers[id] = ch
	b.mu.Unlock()
	var once sync.Once
	cancel := func() { once.Do(func(){ b.mu.Lock(); delete(b.subscribers,id); close(ch); b.mu.Unlock() }) }
	return ch, cancel
}

func writeRequestNotice(w interface{ Write([]byte)(int,error) }, notice RequestNotice) {
	_, _ = w.Write([]byte("data: "))
	data, err := json.Marshal(notice)
	if err != nil { return }
	_, _ = w.Write(data)
	_, _ = w.Write([]byte("\n\n"))
}

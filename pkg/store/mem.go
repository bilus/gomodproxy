package store

import (
	"context"
	"errors"
	"sync"

	"github.com/bilus/gomodproxy/pkg/vcs"
)

type memory struct {
	sync.Mutex
	log   logger
	limit int64
	size  int64
	head  *lruItem
	tail  *lruItem
}

type lruItem struct {
	Snapshot
	prev *lruItem
	next *lruItem
}

// Memory creates an in-memory LRU cache.
func Memory(log logger, limit int64) Store { return &memory{log: log, limit: limit} }

func (m *memory) Put(ctx context.Context, snapshot Snapshot) error {
	m.Lock()
	defer m.Unlock()
	if _, err := m.lookup(snapshot.Module, snapshot.Version); err == nil {
		return nil
	}

	item := &lruItem{Snapshot: snapshot, next: m.head}
	m.insert(item)

	for m.limit >= 0 && m.size > m.limit {
		m.evict()
	}
	return nil
}

func (m *memory) Get(ctx context.Context, module string, version vcs.Version) (Snapshot, error) {
	m.Lock()
	defer m.Unlock()
	item, err := m.lookup(module, version)
	if err != nil {
		return Snapshot{}, err
	}
	return item.Snapshot, nil
}

func (m *memory) Del(ctx context.Context, module string, version vcs.Version) error {
	m.Lock()
	defer m.Unlock()
	item, err := m.lookup(module, version)
	if err != nil {
		return err
	}
	if item.prev == nil {
		m.head = item.next
	} else {
		item.prev.next = item.next
	}
	if item.next == nil {
		m.tail = item.prev
	} else {
		item.next.prev = item.prev
	}
	return nil
}

func (m *memory) lookup(module string, version vcs.Version) (*lruItem, error) {
	for item := m.head; item != nil; item = item.next {
		if item.Module == module && item.Version == version {
			m.update(item)
			return item, nil
		}
	}
	return nil, errors.New("not found")
}

func (m *memory) insert(item *lruItem) {
	m.log("mem.insert",
		"module", item.Module, "version", item.Version, "size", len(item.Data),
		"cachesize", m.size, "cachelimit", m.limit)
	m.size = m.size + int64(len(item.Data))
	if m.head == nil {
		m.head = item
		m.tail = item
		return
	}
	item.next = m.head
	m.head.prev = item
	m.head = item
}

func (m *memory) update(item *lruItem) {
	m.log("mem.update", "module", item.Module, "version", item.Version, "size", len(item.Data),
		"cachesize", m.size, "cachelimit", m.limit)
	if item.prev == nil {
		return
	}
	item.prev.next = item.next
	if item.next == nil {
		m.tail = item.prev
	} else {
		item.next.prev = item.prev
	}
	item.prev = nil
	item.next = m.head
	m.head.prev = item
	m.head = item
}

func (m *memory) evict() {
	m.log("mem.evict", "module", m.tail.Module, "version", m.tail.Version, "size", len(m.tail.Data),
		"cachesize", m.size, "cachelimit", m.limit)
	m.size = m.size - int64(len(m.tail.Data))
	if m.tail.prev == nil {
		m.head = nil
		m.tail = nil
		return
	}
	m.tail.prev.next = nil
	m.tail = m.tail.prev
}

func (m *memory) Close() error { return nil }

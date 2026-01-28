package shelltool

import (
	"container/list"
	"sync"
	"time"
)

type shellSession struct {
	id      string
	workdir string
	env     map[string]string
	mu      sync.RWMutex
	closed  bool
}

type sessionStore struct {
	mu  sync.Mutex
	ttl time.Duration
	max int

	lru *list.List               // front=most recently used
	m   map[string]*list.Element // id -> *list.Element(Value=*sessionItem)
}

type sessionItem struct {
	s        *shellSession
	lastUsed time.Time
}

const (
	defaultSessionTTL  = 30 * time.Minute
	defaultMaxSessions = 256
)

func newSessionStore() *sessionStore {
	return &sessionStore{
		ttl: defaultSessionTTL,
		max: defaultMaxSessions,
		lru: list.New(),
		m:   map[string]*list.Element{},
	}
}

func (ss *sessionStore) setTTL(ttl time.Duration) {
	if ttl < 0 {
		ttl = 0
	}
	ss.mu.Lock()
	ss.ttl = ttl
	ss.evictExpiredLocked(time.Now())
	ss.mu.Unlock()
}

func (ss *sessionStore) setMaxSessions(maxSessions int) {
	if maxSessions < 0 {
		maxSessions = 0
	}
	ss.mu.Lock()
	ss.max = maxSessions
	ss.evictOverLimitLocked()
	ss.mu.Unlock()
}

func (ss *sessionStore) newSession() *shellSession {
	now := time.Now()
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.evictExpiredLocked(now)
	ss.evictOverLimitLocked()

	id := newSessionID()
	s := &shellSession{
		id:      id,
		workdir: "",
		env:     map[string]string{},
	}
	it := &sessionItem{s: s, lastUsed: now}
	e := ss.lru.PushFront(it)
	ss.m[id] = e
	ss.evictOverLimitLocked()

	return s
}

func (ss *sessionStore) get(id string) (*shellSession, bool) {
	now := time.Now()
	ss.mu.Lock()
	ss.evictExpiredLocked(now)
	e, ok := ss.m[id]
	if !ok || e == nil {
		ss.mu.Unlock()
		return nil, false
	}
	it, _ := e.Value.(*sessionItem)
	if it == nil || it.s == nil {
		// Corrupt entry; delete.
		ss.deleteElemLocked(e)
		ss.mu.Unlock()
		return nil, false
	}
	it.lastUsed = now
	ss.lru.MoveToFront(e)
	s := it.s
	ss.mu.Unlock()

	s.mu.RLock()
	closed := s.closed
	s.mu.RUnlock()
	if closed {
		return nil, false
	}
	return s, true
}

func (ss *sessionStore) delete(id string) {
	ss.mu.Lock()
	e := ss.m[id]
	if e != nil {
		ss.deleteElemLocked(e)
	}
	ss.mu.Unlock()
}

func (ss *sessionStore) evictExpiredLocked(now time.Time) {
	if ss.ttl <= 0 {
		return
	}
	// Oldest at back; stop once we find a non-expired entry.
	for e := ss.lru.Back(); e != nil; {
		prev := e.Prev()
		it, ok := e.Value.(*sessionItem)
		if !ok || it == nil {
			// Corrupt entry; delete defensively.
			ss.deleteElemLocked(e)
			e = prev
			continue
		}
		if now.Sub(it.lastUsed) <= ss.ttl {
			break
		}
		ss.deleteElemLocked(e)
		e = prev
	}
}

func (ss *sessionStore) evictOverLimitLocked() {
	if ss.max <= 0 {
		return
	}
	for ss.lru.Len() > ss.max {
		if e := ss.lru.Back(); e != nil {
			ss.deleteElemLocked(e)
		} else {
			return
		}
	}
}

func (ss *sessionStore) deleteElemLocked(e *list.Element) {
	if e == nil {
		return
	}
	it, _ := e.Value.(*sessionItem)
	if it == nil || it.s == nil {
		ss.lru.Remove(e)
		return
	}
	delete(ss.m, it.s.id)
	ss.lru.Remove(e)

	// Mark closed.
	it.s.mu.Lock()
	it.s.closed = true
	it.s.mu.Unlock()
}

func (ss *sessionStore) sizeForTest() int {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return len(ss.m)
}

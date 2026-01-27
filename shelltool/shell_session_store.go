package shelltool

import "sync"

type shellSession struct {
	id      string
	workdir string
	env     map[string]string
	mu      sync.RWMutex
	closed  bool
}

type sessionStore struct {
	mu sync.RWMutex
	m  map[string]*shellSession
}

func newSessionStore() *sessionStore {
	return &sessionStore{m: map[string]*shellSession{}}
}

func (ss *sessionStore) newSession() *shellSession {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	id := newSessionID()
	s := &shellSession{
		id:      id,
		workdir: "",
		env:     map[string]string{},
	}
	ss.m[id] = s
	return s
}

func (ss *sessionStore) get(id string) (*shellSession, bool) {
	ss.mu.RLock()
	s, ok := ss.m[id]
	ss.mu.RUnlock()
	if !ok || s == nil {
		return nil, false
	}
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
	s := ss.m[id]
	delete(ss.m, id)
	ss.mu.Unlock()
	if s != nil {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
	}
}

func (ss *sessionStore) reset(s *shellSession) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.workdir = ""
	s.env = map[string]string{}
}

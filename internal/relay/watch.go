package relay

type mailWatch struct {
	changed chan struct{}
	refs    int
}

// Only active listeners allocate wake state; unrelated mailboxes never share a
// broadcast, and the final listener removes the entry.
func (s *Server) watchMailbox(mailbox string) (*mailWatch, func()) {
	s.mu.Lock()
	w := s.mailChanges[mailbox]
	if w == nil {
		w = &mailWatch{changed: make(chan struct{})}
		s.mailChanges[mailbox] = w
	}
	w.refs++
	s.mu.Unlock()
	return w, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.refs--
		if w.refs == 0 {
			delete(s.mailChanges, mailbox)
		}
	}
}
func (s *Server) changes(w *mailWatch) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return w.changed
}
func (s *Server) signal(mailbox string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w := s.mailChanges[mailbox]; w != nil {
		close(w.changed)
		w.changed = make(chan struct{})
	}
}

func (s *Server) reservePoll(key, mailbox string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.keyPolls[key] >= s.cfg.Limits.PollsPerKey || s.mailPolls[mailbox] >= s.cfg.Limits.PollsPerMailbox {
		return false
	}
	s.keyPolls[key]++
	s.mailPolls[mailbox]++
	return true
}
func (s *Server) releasePoll(key, mailbox string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keyPolls[key]--
	s.mailPolls[mailbox]--
	if s.keyPolls[key] == 0 {
		delete(s.keyPolls, key)
	}
	if s.mailPolls[mailbox] == 0 {
		delete(s.mailPolls, mailbox)
	}
}

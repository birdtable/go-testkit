package testkit

import (
	"io"
	"sync"
)

// stream is one stream of a started program, kept whole as it comes. Its Write never
// blocks, so the program never waits on a test that does not read, and each reader reads it from its
// start, waiting for more until the stream ends.
type stream struct {
	mu   sync.Mutex
	more *sync.Cond
	b    []byte
	end  bool
}

func newStream() *stream {
	s := &stream{}
	s.more = sync.NewCond(&s.mu)
	return s
}

// Write keeps p and wakes every reader that waits.
func (s *stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.b = append(s.b, p...)
	s.mu.Unlock()
	s.more.Broadcast()
	return len(p), nil
}

// close ends the stream: a reader that read all of it then gets io.EOF.
func (s *stream) close() {
	s.mu.Lock()
	s.end = true
	s.mu.Unlock()
	s.more.Broadcast()
}

// String is all the stream holds now.
func (s *stream) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.b)
}

// reader is a new reader of the stream, from its start.
func (s *stream) reader() io.Reader { return &streamReader{s: s} }

// streamReader reads its stream from where it has read to.
type streamReader struct {
	s   *stream
	off int
}

// Read gives what the stream holds past what this reader read, and waits while there is nothing
// more and the stream has not ended. Once it ended and all of it was read, Read is io.EOF.
func (r *streamReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	for r.off == len(s.b) && !s.end {
		s.more.Wait()
	}
	if r.off == len(s.b) {
		return 0, io.EOF
	}
	n := copy(p, s.b[r.off:])
	r.off += n
	return n, nil
}

package scmserver

import (
	"net/http"
	"sync"
	"time"
)

const maxRequestLog = 20000

// RequestRecord is one request handled by a provider listener.
type RequestRecord struct {
	Time       time.Time `json:"time"`
	Provider   Provider  `json:"provider"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Query      string    `json:"query,omitempty"`
	Principal  string    `json:"principal,omitempty"`
	Duration   Duration  `json:"duration"`
	StatusCode int       `json:"statusCode"`
	Fault      bool      `json:"fault,omitempty"`
	Git        bool      `json:"git,omitempty"`
}

type requestLog struct {
	records []RequestRecord
	mu      sync.Mutex
}

func (l *requestLog) add(rec RequestRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, rec)
	if len(l.records) > maxRequestLog {
		l.records = l.records[len(l.records)-maxRequestLog:]
	}
}

func (l *requestLog) list() []RequestRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]RequestRecord(nil), l.records...)
}

func (l *requestLog) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = nil
}

// recorder captures the status code written by a handler.
type recorder struct {
	http.ResponseWriter
	principal string
	status    int
	fault     bool
	git       bool
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	if err != nil {
		return n, err //nolint:wrapcheck // pass-through ResponseWriter
	}
	return n, nil
}

func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *recorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

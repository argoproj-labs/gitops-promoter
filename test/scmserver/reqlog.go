package scmserver

import (
	"net/http"
	"sort"
	"sync"
	"time"
)

const maxRequestLog = 20000

// RequestRecord is one request handled by a provider listener.
type RequestRecord struct {
	Time     time.Time `json:"time"`
	Provider Provider  `json:"provider"`
	Method   string    `json:"method"`
	Path     string    `json:"path"`
	Query    string    `json:"query,omitempty"`
	// Route is the route that served the request, so requests can be counted without parsing paths: the pattern of
	// a REST API route, such as "GET /api/v3/repos/{owner}/{repo}/pulls" ("/" when no route matched), or a synthetic
	// route for git smart HTTP, such as "GIT GET /info/refs?service=git-upload-pack" and "GIT POST /git-upload-pack".
	Route     string `json:"route,omitempty"`
	Principal string `json:"principal,omitempty"`
	// Credential identifies the credential that authenticated the request, without its secret: "app:<id>" for a
	// GitHub App JWT, "installation:<id>" for a GitHub App installation token, "user:<name>" for basic auth, or
	// "token" for an access token. It is the unit a provider rate limits. Empty for unauthenticated requests.
	Credential string `json:"credential,omitempty"`
	// UserAgent is the request's User-Agent header.
	UserAgent string `json:"userAgent,omitempty"`
	// Seq numbers the requests of a server in the order they completed, starting at 1. It keeps counting when the
	// log is cleared, so it can be used as a cursor (GET /api/requests?after=<seq>).
	Seq        int64    `json:"seq"`
	Duration   Duration `json:"duration"`
	StatusCode int      `json:"statusCode"`
	Fault      bool     `json:"fault,omitempty"`
	Git        bool     `json:"git,omitempty"`
	// NoFault is true for requests carrying NoFaultHeader, which test fixtures send.
	NoFault bool `json:"noFault,omitempty"`
}

type requestLog struct {
	records []RequestRecord
	seq     int64
	mu      sync.Mutex
}

func (l *requestLog) add(rec RequestRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	rec.Seq = l.seq
	l.records = append(l.records, rec)
	if len(l.records) > maxRequestLog {
		l.records = l.records[len(l.records)-maxRequestLog:]
	}
}

func (l *requestLog) list() []RequestRecord {
	return l.after(0)
}

// after returns the records with a sequence number above seq, oldest first.
func (l *requestLog) after(seq int64) []RequestRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	i := sort.Search(len(l.records), func(i int) bool { return l.records[i].Seq > seq })
	return append([]RequestRecord(nil), l.records[i:]...)
}

// reset clears the log. Sequence numbers keep counting, so a cursor taken before the reset stays valid.
func (l *requestLog) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = nil
}

// recorder captures the status code written by a handler.
type recorder struct {
	http.ResponseWriter
	principal  string
	credential string
	status     int
	fault      bool
	git        bool
}

// authenticatedAs records the caller a handler authenticated.
func (r *recorder) authenticatedAs(who principal) {
	r.principal = who.name
	r.credential = who.credential()
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

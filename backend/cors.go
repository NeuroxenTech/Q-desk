package main

import (
	"bufio"
	"io"
	"net"
	"net/http"
)

// allowedOrigin reports whether the given origin exactly matches an entry in
// the configured allowlist. Comparisons are exact string matches — no wildcard
// and no suffix/prefix matching.
func (a *app) allowedOrigin(origin string) bool {
	for _, allowed := range a.cfg.AllowedOrigins {
		if origin == allowed {
			return true
		}
	}
	return false
}

// corsMiddleware enforces a strict origin allowlist. It only sets
// Access-Control-Allow-Origin to the exact requesting Origin header when that
// value matches an allowlist entry. If the Origin is not allowlisted, no CORS
// header is set at all so the browser's same-origin policy blocks the request.
// '*' is never used.
func (a *app) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		if origin != "" && a.allowedOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}

		// Reject CORS preflight OPTIONS from non-allowlisted origins outright.
		if r.Method == http.MethodOptions {
			if origin == "" || !a.allowedOrigin(origin) {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Hijack forwards the interface so the WebSocket upgrader can take over the
// underlying connection (the wrapping response writer would otherwise fail the
// upgrade with "response does not implement http.Hijacker").
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return s.ResponseWriter.(http.Hijacker).Hijack()
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) ReadFrom(r io.Reader) (int64, error) {
	if rf, ok := s.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(r)
	}
	return io.Copy(struct{ io.Writer }{s.ResponseWriter}, r)
}

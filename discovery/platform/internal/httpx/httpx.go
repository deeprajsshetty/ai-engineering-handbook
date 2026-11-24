// Package httpx holds HTTP middleware and helpers: request IDs, structured
// access logs, panic recovery, security headers, RFC 9457 problem responses,
// JSON encoding and a per-principal token-bucket rate limiter.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/ids"
	"github.com/deeprajsshetty/observability-platform/platform/internal/telemetry"
)

type ctxKey int

const requestIDKey ctxKey = 1

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// RequestID returns the request's correlation ID.
func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

// WithRequestID stores id in ctx.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

type recorder struct {
	http.ResponseWriter
	status int
	bytes  int
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
	r.bytes += n
	return n, err
}

// LogFields lets inner handlers add fields (tenant, subject) to the access log.
type LogFields struct {
	mu     sync.Mutex
	fields []any
}

// Add appends key/value pairs.
func (l *LogFields) Add(kv ...any) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.fields = append(l.fields, kv...)
	l.mu.Unlock()
}

const logFieldsKey ctxKey = 2

// Fields returns the access-log field collector of the request.
func Fields(ctx context.Context) *LogFields {
	l, _ := ctx.Value(logFieldsKey).(*LogFields)
	return l
}

// Observe wraps h with request ID, recovery, security headers and access logging.
func Observe(h http.Handler, log *slog.Logger, tel *telemetry.Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := r.Header.Get("X-Request-ID")
		if !requestIDPattern.MatchString(id) {
			id = ids.New("req")
		}
		w.Header().Set("X-Request-ID", id)
		SecurityHeaders(w.Header())
		lf := &LogFields{}
		ctx := context.WithValue(WithRequestID(r.Context(), id), logFieldsKey, lf)
		rec := &recorder{ResponseWriter: w}
		inner := r.WithContext(ctx) // ServeMux records the matched pattern on this request
		defer func() {
			if p := recover(); p != nil {
				log.Error("panic serving request", "requestId", id, "panic", fmt.Sprint(p))
				if rec.status == 0 {
					Problem(rec, inner, http.StatusInternalServerError, "internal", "internal server error", nil)
				}
			}
			route := inner.Pattern
			if route == "" {
				route = "unmatched"
			}
			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			dur := time.Since(start)
			if tel != nil {
				tel.Add("obs_http_requests_total", 1, "method", r.Method, "route", route, "code", strconv.Itoa(status))
				tel.Add("obs_http_request_duration_ms_sum", dur.Milliseconds(), "route", route)
			}
			if strings.HasPrefix(r.URL.Path, "/healthz") || strings.HasPrefix(r.URL.Path, "/readyz") {
				return // keep probe noise out of logs
			}
			lf.mu.Lock()
			fields := append([]any{"requestId", id, "method", r.Method, "route", route, "status", status,
				"durationMs", dur.Milliseconds(), "bytes", rec.bytes}, lf.fields...)
			lf.mu.Unlock()
			lvl := slog.LevelInfo
			if status >= 500 {
				lvl = slog.LevelError
			}
			log.Log(ctx, lvl, "http request", fields...)
		}()
		h.ServeHTTP(rec, inner)
	})
}

// SecurityHeaders sets defensive response headers.
func SecurityHeaders(h http.Header) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
}

// FieldError is one validation failure.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ProblemBody is an RFC 9457 problem document with extension members.
type ProblemBody struct {
	Type      string         `json:"type"`
	Title     string         `json:"title"`
	Status    int            `json:"status"`
	Detail    string         `json:"detail,omitempty"`
	Code      string         `json:"code"`
	RequestID string         `json:"requestId"`
	Errors    []FieldError   `json:"errors,omitempty"`
	Extra     map[string]any `json:"extra,omitempty"`
}

// Problem writes application/problem+json.
func Problem(w http.ResponseWriter, r *http.Request, status int, code, detail string, errs []FieldError) {
	ProblemExtra(w, r, status, code, detail, errs, nil)
}

// ProblemExtra writes a problem with extension data.
func ProblemExtra(w http.ResponseWriter, r *http.Request, status int, code, detail string, errs []FieldError, extra map[string]any) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ProblemBody{
		Type: "urn:obsplatform:problem:" + code, Title: http.StatusText(status), Status: status,
		Detail: detail, Code: code, RequestID: RequestID(r.Context()), Errors: errs, Extra: extra,
	})
}

// JSON writes v with status.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

// ErrBodyTooLarge is returned by DecodeJSON for oversized bodies.
var ErrBodyTooLarge = errors.New("request body too large")

// DecodeJSON decodes exactly one JSON object, rejecting unknown fields and
// bodies over max bytes.
func DecodeJSON(w http.ResponseWriter, r *http.Request, max int64, v any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
		return errors.New("Content-Type must be application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, max)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return ErrBodyTooLarge
		}
		if errors.Is(err, io.EOF) {
			return errors.New("request body is empty")
		}
		return fmt.Errorf("invalid JSON: %s", sanitizeJSONErr(err))
	}
	if dec.More() {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}

func sanitizeJSONErr(err error) string {
	s := err.Error()
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// RateLimiter is a token bucket per key. Idle buckets are evicted.
type RateLimiter struct {
	rate    float64
	burst   float64
	mu      sync.Mutex
	buckets map[string]*bucket
	lastGC  time.Time
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewRateLimiter allows rps requests per second with the given burst.
func NewRateLimiter(rps float64, burst int) *RateLimiter {
	return &RateLimiter{rate: rps, burst: float64(burst), buckets: map[string]*bucket{}, now: time.Now}
}

// Allow consumes a token for key; when refused it returns the wait time.
func (l *RateLimiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.lastGC) > time.Minute {
		for k, b := range l.buckets {
			if now.Sub(b.last) > 10*time.Minute {
				delete(l.buckets, k)
			}
		}
		l.lastGC = now
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
}

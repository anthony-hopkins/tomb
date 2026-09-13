package platform

import (
	"context"
	"database/sql"
	"net/http"
	"time"
)

// Health serves the liveness and readiness endpoints Principle VI requires for
// container orchestration and uptime checks.
type Health struct {
	DB *sql.DB
}

// Live answers GET /healthz.
//
// It performs no database work on purpose: a database outage must not make
// Cloud Run kill containers that are otherwise serving fine
// (contracts/http-routes.md).
func (h *Health) Live(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// Ready answers GET /readyz, reporting 503 when the database is unreachable.
func (h *Health) Ready(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	if h.DB == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("no database configured"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := h.DB.PingContext(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		// The reason is safe to expose: it names no credential.
		w.Write([]byte("database unreachable"))
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

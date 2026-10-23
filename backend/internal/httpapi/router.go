package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

const basePath = "/api/v1"

// Deps are the dependencies of the HTTP handler.
type Deps struct {
	Ledger LedgerService
	Family FamilyService
	Budget BudgetService
	// Auth identifies the user of every API request.
	Auth *Auth
	// Location is the timezone of users who have not chosen one.
	Location *time.Location
	// Health reports whether the service can serve requests (e.g. the database is reachable).
	Health func(ctx context.Context) error
	Log    *slog.Logger
}

// NewHandler builds the complete HTTP handler: the generated API routes,
// the OpenAPI spec with Swagger UI, and the health check.
func NewHandler(d Deps) http.Handler {
	mux := http.NewServeMux()

	strict := NewStrictHandlerWithOptions(NewServer(d.Ledger, d.Family, d.Budget, d.Location), nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) { writeBadRequest(w, err) },
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeError(d.Log, w, r, err)
		},
	})
	HandlerWithOptions(strict, StdHTTPServerOptions{
		BaseURL:    basePath,
		BaseRouter: mux,
		// Every API operation needs a Telegram user; the spec, docs and health check do not.
		Middlewares:      []MiddlewareFunc{d.Auth.Middleware},
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) { writeBadRequest(w, err) },
	})

	mux.HandleFunc("GET "+basePath+"/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		spec, err := GetSwagger()
		if err != nil {
			writeError(d.Log, w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, spec)
	})
	mux.HandleFunc("GET /docs", serveDocs)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := d.Health(r.Context()); err != nil {
			d.Log.WarnContext(r.Context(), "health check failed", "err", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	return recoverer(d.Log, accessLog(d.Log, mux))
}

// serveDocs renders Swagger UI for the spec, loaded from a CDN.
func serveDocs(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <title>Savepoint API</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui-bundle.js"></script>
  <script>SwaggerUIBundle({ url: "` + basePath + `/openapi.json", dom_id: "#swagger-ui" });</script>
</body>
</html>`))
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func accessLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/healthz" {
			return
		}
		log.InfoContext(r.Context(), "http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start).Round(time.Microsecond))
	})
}

func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.ErrorContext(r.Context(), "panic in handler", "panic", v, "stack", string(debug.Stack()))
				writeJSON(w, http.StatusInternalServerError,
					Error{Error: ErrorDetail{Code: ErrCodeInternal, Message: "internal server error"}})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

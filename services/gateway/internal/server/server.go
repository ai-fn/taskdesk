package server

import (
	"bufio"
	"log/slog"
	"net"
	"net/http"

	"github.com/fsociety/todo/services/gateway/internal/handler"
	"github.com/fsociety/todo/services/gateway/internal/middleware"
	aiv1 "github.com/fsociety/todo/proto/gen/ai/v1"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"
)

// New returns the gateway's HTTP handler with all routes mounted.
func New(
	log *slog.Logger,
	authClient authv1.AuthServiceClient,
	todoClient todov1.TodoServiceClient,
	aiClient aiv1.AIServiceClient,
	rdb *redis.Client,
) http.Handler {
	r := chi.NewRouter()

	r.Use(chimw.Recoverer)
	r.Use(accessLog(log))

	// Public
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	auth := handler.NewAuthHandler(authClient)
	r.Route("/api/v1/auth", func(r chi.Router) {
		r.Post("/signup",  auth.SignUp)
		r.Post("/login",   auth.Login)
		r.Post("/refresh", auth.Refresh)
		r.Post("/logout",  auth.Logout)
	})

	todo := handler.NewTodoHandler(todoClient)
	r.Route("/api/v1/tasks", func(r chi.Router) {
		r.Use(middleware.Authenticate(authClient))

		r.Get("/",                   todo.List)
		r.Post("/",                  todo.Create)
		r.Get("/{taskID}",           todo.Get)
		r.Patch("/{taskID}",         todo.Update)
		r.Delete("/{taskID}",        todo.Delete)
		r.Post("/{taskID}/complete", todo.Complete)
	})

	// Daily plan (AI-generated)
	plan := handler.NewPlanHandler(aiClient)
	r.With(middleware.Authenticate(authClient)).Get("/api/v1/plan", plan.GetLatest)

	// WebSocket: authenticated users subscribe to their real-time notifications.
	ws := handler.NewWSHandler(rdb, log)
	r.With(middleware.Authenticate(authClient)).Get("/api/v1/ws", ws.ServeHTTP)

	return r
}

func accessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)
			log.Info("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rw.status,
				"remote", r.RemoteAddr,
			)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}

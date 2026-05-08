package server

import (
	"log/slog"
	"net/http"

	"github.com/fsociety/todo/services/gateway/internal/handler"
	"github.com/fsociety/todo/services/gateway/internal/middleware"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

// New returns the gateway's HTTP handler with all routes mounted.
func New(
	log *slog.Logger,
	authClient authv1.AuthServiceClient,
	todoClient todov1.TodoServiceClient,
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

		r.Get("/",                  todo.List)
		r.Post("/",                 todo.Create)
		r.Get("/{taskID}",          todo.Get)
		r.Patch("/{taskID}",        todo.Update)
		r.Delete("/{taskID}",       todo.Delete)
		r.Post("/{taskID}/complete", todo.Complete)
	})

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

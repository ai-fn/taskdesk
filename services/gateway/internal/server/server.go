package server

import (
	"bufio"
	"log/slog"
	"net"
	"net/http"

	pkgmetrics "github.com/fsociety/todo/pkg/metrics"
	"github.com/fsociety/todo/services/gateway/internal/handler"
	"github.com/fsociety/todo/services/gateway/internal/middleware"
	"github.com/fsociety/todo/services/gateway/internal/oauth"
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
	oauthProviders oauth.Registry,
) http.Handler {
	r := chi.NewRouter()

	r.Use(chimw.Recoverer)
	r.Use(accessLog(log))
	r.Use(pkgmetrics.HTTPMiddleware)

	// Public
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	auth := handler.NewAuthHandler(authClient, todoClient, log)
	oauthH := handler.NewOAuthHandler(authClient, oauthProviders, log)
	r.Route("/api/v1/auth", func(r chi.Router) {
		r.Post("/signup",  auth.SignUp)
		r.Post("/login",   auth.Login)
		r.Post("/refresh", auth.Refresh)
		r.Post("/logout",  auth.Logout)

		r.Get("/oauth/{provider}",          oauthH.Start)
		r.Get("/oauth/{provider}/callback", oauthH.Callback)
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
		r.Post("/{taskID}/restore",  todo.Restore)
		r.Delete("/{taskID}/purge",  todo.Purge)

		r.Get("/{taskID}/dependencies",                  todo.ListDependencies)
		r.Get("/{taskID}/dependency-tree",               todo.GetDependencyTree)
		r.Post("/{taskID}/dependencies",                 todo.AddDependency)
		r.Delete("/{taskID}/dependencies/{dependsOnID}", todo.RemoveDependency)
	})

	lists := handler.NewListHandler(todoClient)
	r.Route("/api/v1/lists", func(r chi.Router) {
		r.Use(middleware.Authenticate(authClient))

		r.Get("/",              lists.List)
		r.Post("/",             lists.Create)
		r.Post("/reorder",      lists.Reorder)
		r.Patch("/{listID}",    lists.Update)
		r.Delete("/{listID}",   lists.Delete)
	})

	groups := handler.NewGroupHandler(todoClient)
	r.Route("/api/v1/groups", func(r chi.Router) {
		r.Use(middleware.Authenticate(authClient))

		r.Get("/",              groups.List)
		r.Post("/",             groups.Create)
		r.Post("/reorder",      groups.Reorder)
		r.Patch("/{groupID}",   groups.Update)
		r.Delete("/{groupID}",  groups.Delete)
	})

	// Daily plan (AI-generated)
	plan := handler.NewPlanHandler(aiClient)
	r.Route("/api/v1/plan", func(r chi.Router) {
		r.Use(middleware.Authenticate(authClient))
		r.Get("/", plan.GetLatest)
		r.Post("/generate", plan.Generate)
	})

	// Semantic search (pgvector)
	search := handler.NewSearchHandler(aiClient, todoClient)
	r.With(middleware.Authenticate(authClient)).Get("/api/v1/tasks/search", search.Search)

	// Natural language task parsing
	parseH := handler.NewParseHandler(aiClient)
	r.With(middleware.Authenticate(authClient)).Post("/api/v1/tasks/parse", parseH.Parse)

	sessions := handler.NewSessionsHandler(authClient)
	r.Route("/api/v1/sessions", func(r chi.Router) {
		r.Use(middleware.Authenticate(authClient))
		r.Get("/", sessions.List)
		r.Delete("/{sessionID}", sessions.Revoke)
		r.Post("/revoke-others", sessions.RevokeOthers)
	})

	account := handler.NewAccountHandler(authClient)
	r.Route("/api/v1/account", func(r chi.Router) {
		r.Use(middleware.Authenticate(authClient))
		r.Get("/me", account.Me)
		r.Patch("/language", account.UpdateLanguage)
	})

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

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}

package server

import (
	"bufio"
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	pkgmetrics "github.com/fsociety/todo/pkg/metrics"
	aiv1 "github.com/fsociety/todo/proto/gen/ai/v1"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	todov1 "github.com/fsociety/todo/proto/gen/todo/v1"
	gwauth "github.com/fsociety/todo/services/gateway/internal/auth"
	"github.com/fsociety/todo/services/gateway/internal/handler"
	"github.com/fsociety/todo/services/gateway/internal/middleware"
	"github.com/fsociety/todo/services/gateway/internal/oauth"
	"github.com/fsociety/todo/services/gateway/internal/ws"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

// maxBodyBytes caps every request body; the biggest legitimate payload is a Stripe webhook.
const maxBodyBytes = 64 << 10

// New returns the gateway's HTTP handler with all routes mounted, and a cleanup
// function that stops background goroutines started by the handlers.
// Call cleanup() after the HTTP server has stopped accepting requests.
func New(
	log *slog.Logger,
	authClient authv1.AuthServiceClient,
	todoClient todov1.TodoServiceClient,
	aiClient aiv1.AIServiceClient,
	rdb *redis.Client,
	oauthProviders oauth.Registry,
	cookieSecure bool,
) (http.Handler, func(context.Context)) {
	r := chi.NewRouter()

	r.Use(chimw.Recoverer)
	r.Use(accessLog(log))
	r.Use(pkgmetrics.HTTPMiddleware(middleware.RoutePattern))

	// Per-pod limiters (not shared across replicas); stopped by cleanup.
	var limiters []*handler.RateLimiter
	newLimiter := func(limit rate.Limit, burst int, idle time.Duration) *handler.RateLimiter {
		l := handler.NewRateLimiter(limit, burst, idle, min(idle, 15*time.Second)) // sweep often so spoofed-key floods stay small
		limiters = append(limiters, l)
		return l
	}
	r.Use(middleware.MaxBody(maxBodyBytes))
	// IP buckets refill in seconds, so entries idle for 1 min are dropped to bound memory.
	r.Use(middleware.RateLimitIP(newLimiter(100, 200, time.Minute), "/healthz")) // 100 req/s per IP, burst 200
	authIP := middleware.RateLimitIP(newLimiter(10, 20, time.Minute))            // signup/login: 10 req/s per IP
	// Paid-API endpoints are limited per user, since each call costs Claude or Voyage money.
	parseLimit := middleware.RateLimitUser(newLimiter(rate.Every(3*time.Second), 5, 15*time.Minute))     // 20/min
	searchLimit := middleware.RateLimitUser(newLimiter(rate.Every(time.Second), 10, 15*time.Minute))     // 60/min
	generateLimit := middleware.RateLimitUser(newLimiter(rate.Every(10*time.Second), 3, 15*time.Minute)) // 6/min
	confirmLimit := middleware.RateLimitUser(newLimiter(rate.Every(12*time.Second), 5, 15*time.Minute))  // 5/min: OTP guesses

	// Public
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Local JWT verifier: validates tokens with a cached signing key and serves
	// user profiles from a short-TTL cache, keeping auth off the per-request
	// gRPC hot path. Shared across every authenticated mount below.
	verifier := gwauth.NewVerifier(authClient)

	// Per-pod WebSocket hub: one Redis PubSub fanned out to all connections,
	// with dynamic per-user (un)subscribe and slow-consumer eviction.
	hub := ws.NewHub(rdb.Subscribe(context.Background()), log)
	hub.Run()

	auth := handler.NewAuthHandler(authClient, todoClient, log, cookieSecure)
	cleanup := func(ctx context.Context) {
		// Drain hijacked WebSockets first (http.Server.Shutdown does not), then
		// stop the cache janitors.
		hub.Shutdown(ctx)
		auth.Stop()
		verifier.Stop()
		for _, l := range limiters {
			l.Stop()
		}
	}
	oauthH := handler.NewOAuthHandler(authClient, oauthProviders, log, cookieSecure)
	r.Route("/api/v1/auth", func(r chi.Router) {
		r.With(authIP).Post("/signup", auth.SignUp)
		r.With(authIP).Post("/login", auth.Login)
		r.Post("/refresh", auth.Refresh)
		r.Post("/logout", auth.Logout)

		r.Post("/password/forgot", auth.ForgotPassword)
		r.Post("/password/reset", auth.ResetPassword)

		r.Get("/oauth/{provider}", oauthH.Start)
		r.Get("/oauth/{provider}/callback", oauthH.Callback)
	})

	todo := handler.NewTodoHandler(todoClient)
	r.Route("/api/v1/tasks", func(r chi.Router) {
		r.Use(middleware.Authenticate(verifier))

		r.Get("/", todo.List)
		r.Post("/", todo.Create)
		r.Get("/{taskID}", todo.Get)
		r.Patch("/{taskID}", todo.Update)
		r.Delete("/{taskID}", todo.Delete)
		r.Post("/{taskID}/complete", todo.Complete)
		r.Post("/{taskID}/restore", todo.Restore)
		r.Delete("/{taskID}/purge", todo.Purge)

		r.Get("/{taskID}/dependencies", todo.ListDependencies)
		r.Get("/{taskID}/dependency-tree", todo.GetDependencyTree)
		r.Post("/{taskID}/dependencies", todo.AddDependency)
		r.Delete("/{taskID}/dependencies/{dependsOnID}", todo.RemoveDependency)
	})

	lists := handler.NewListHandler(todoClient)
	r.Route("/api/v1/lists", func(r chi.Router) {
		r.Use(middleware.Authenticate(verifier))

		r.Get("/", lists.List)
		r.Post("/", lists.Create)
		r.Post("/reorder", lists.Reorder)
		r.Patch("/{listID}", lists.Update)
		r.Delete("/{listID}", lists.Delete)
	})

	groups := handler.NewGroupHandler(todoClient)
	r.Route("/api/v1/groups", func(r chi.Router) {
		r.Use(middleware.Authenticate(verifier))

		r.Get("/", groups.List)
		r.Post("/", groups.Create)
		r.Post("/reorder", groups.Reorder)
		r.Patch("/{groupID}", groups.Update)
		r.Delete("/{groupID}", groups.Delete)
	})

	// Daily plan (AI-generated) — pro only
	plan := handler.NewPlanHandler(aiClient)
	r.Route("/api/v1/plan", func(r chi.Router) {
		r.Use(middleware.Authenticate(verifier))
		r.Use(middleware.RequirePlan("pro"))
		r.Get("/", plan.GetLatest)
		r.With(middleware.OnePerUser(), generateLimit).Post("/generate", plan.Generate)
	})

	// Semantic search (pgvector) — available to all; pro uses vector, free uses fulltext.
	search := handler.NewSearchHandler(aiClient, todoClient)
	r.With(middleware.Authenticate(verifier), searchLimit).Get("/api/v1/tasks/search", search.Search)

	// Natural language task parsing — pro only
	parseH := handler.NewParseHandler(aiClient)
	r.Route("/api/v1/tasks/parse", func(r chi.Router) {
		r.Use(middleware.Authenticate(verifier))
		r.Use(middleware.RequirePlan("pro"))
		r.Use(parseLimit)
		r.Post("/", parseH.Parse)
	})

	// Subscriptions
	sub := handler.NewSubscriptionHandler(authClient)
	r.Route("/api/v1/subscriptions", func(r chi.Router) {
		r.Use(middleware.Authenticate(verifier))
		r.Get("/", sub.GetSubscription)
		r.Post("/redeem", sub.RedeemPromoCode)
		r.Post("/checkout", sub.CreateCheckout)
	})

	// Stripe webhook — no auth middleware; Stripe signs the payload itself.
	stripeH := handler.NewStripeWebhookHandler(authClient)
	r.Post("/api/v1/stripe/webhook", stripeH.Handle)

	sessions := handler.NewSessionsHandler(authClient)
	r.Route("/api/v1/sessions", func(r chi.Router) {
		r.Use(middleware.Authenticate(verifier))
		r.Get("/", sessions.List)
		r.Delete("/{sessionID}", sessions.Revoke)
		r.Post("/revoke-others", sessions.RevokeOthers)
	})

	account := handler.NewAccountHandler(authClient)
	r.Route("/api/v1/account", func(r chi.Router) {
		r.Use(middleware.Authenticate(verifier))
		r.Get("/me", account.Me)
		r.Patch("/language", account.UpdateLanguage)
		r.Patch("/time-zone", account.UpdateTimeZone)
		r.Post("/email/verify/request", account.RequestEmailVerification)
		r.With(confirmLimit).Post("/email/verify/confirm", account.ConfirmEmail)
	})

	// WebSocket: authenticated users subscribe to their real-time notifications.
	wsHandler := handler.NewWSHandler(hub, log)
	r.With(middleware.AuthenticateWS(verifier)).Get("/api/v1/ws", wsHandler.ServeHTTP)

	return r, cleanup
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

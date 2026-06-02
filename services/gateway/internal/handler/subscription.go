package handler

import (
	"encoding/json"
	"io"
	"net/http"

	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	mw "github.com/fsociety/todo/services/gateway/internal/middleware"
)

// SubscriptionHandler bridges HTTP ↔ auth-service subscription RPCs.
type SubscriptionHandler struct {
	client authv1.AuthServiceClient
}

func NewSubscriptionHandler(c authv1.AuthServiceClient) *SubscriptionHandler {
	return &SubscriptionHandler{client: c}
}

// GET /api/v1/subscriptions — current subscription state for the authenticated user.
func (h *SubscriptionHandler) GetSubscription(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())

	resp, err := h.client.GetSubscription(r.Context(), &authv1.GetSubscriptionRequest{
		UserId: userID,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp)
}

// POST /api/v1/subscriptions/redeem — redeem a promo code.
func (h *SubscriptionHandler) RedeemPromoCode(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	email := mw.EmailFromContext(r.Context())

	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if body.Code == "" {
		jsonError(w, "code is required", http.StatusBadRequest)
		return
	}

	resp, err := h.client.RedeemPromoCode(r.Context(), &authv1.RedeemPromoCodeRequest{
		UserId: userID,
		Email:  email,
		Code:   body.Code,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp)
}

// POST /api/v1/subscriptions/checkout — create a Stripe Checkout Session.
func (h *SubscriptionHandler) CreateCheckout(w http.ResponseWriter, r *http.Request) {
	userID := mw.UserIDFromContext(r.Context())
	email := mw.EmailFromContext(r.Context())

	resp, err := h.client.CreateStripeCheckoutSession(r.Context(), &authv1.CreateStripeCheckoutSessionRequest{
		UserId: userID,
		Email:  email,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}
	protoOK(w, resp)
}

// StripeWebhookHandler handles raw Stripe webhook delivery.
// It must be mounted outside the Authenticate middleware — Stripe POSTs without a token.
type StripeWebhookHandler struct {
	client authv1.AuthServiceClient
}

func NewStripeWebhookHandler(c authv1.AuthServiceClient) *StripeWebhookHandler {
	return &StripeWebhookHandler{client: c}
}

// POST /api/v1/stripe/webhook
func (h *StripeWebhookHandler) Handle(w http.ResponseWriter, r *http.Request) {
	// Read raw body — Stripe signature verification requires the exact bytes.
	const maxBodyBytes = 65536
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		jsonError(w, "failed to read body", http.StatusBadRequest)
		return
	}

	sig := r.Header.Get("Stripe-Signature")
	if sig == "" {
		jsonError(w, "missing Stripe-Signature header", http.StatusBadRequest)
		return
	}

	_, err = h.client.HandleStripeWebhook(r.Context(), &authv1.HandleStripeWebhookRequest{
		Payload:   payload,
		Signature: sig,
	})
	if err != nil {
		handleGRPCError(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

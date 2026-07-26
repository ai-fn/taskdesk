CREATE TABLE auth.subscriptions (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES auth.users(id),
  plan       TEXT NOT NULL DEFAULT 'pro',
  source     TEXT NOT NULL,   -- 'promo_code' | 'stripe'
  source_ref TEXT,            -- promo_code_id or stripe_subscription_id
  status     TEXT NOT NULL DEFAULT 'active',  -- 'active' | 'cancelled'
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ,     -- NULL = lifetime
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_subscriptions_user_active
  ON auth.subscriptions(user_id) WHERE status = 'active';

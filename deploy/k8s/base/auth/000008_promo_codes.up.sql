CREATE TABLE auth.promo_codes (
  id                 TEXT PRIMARY KEY,
  code               TEXT NOT NULL UNIQUE,
  allowed_email      TEXT,           -- NULL = any user; set = single-user code
  max_usage_count    INTEGER,        -- NULL = unlimited global uses
  max_usage_per_user INTEGER NOT NULL DEFAULT 1,
  disabled           BOOLEAN NOT NULL DEFAULT false,
  notes              TEXT,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE auth.promo_code_redemptions (
  id            TEXT PRIMARY KEY,
  promo_code_id TEXT NOT NULL REFERENCES auth.promo_codes(id),
  user_id       TEXT NOT NULL REFERENCES auth.users(id),
  redeemed_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Fast per-user count: WHERE promo_code_id=$1 AND user_id=$2
CREATE INDEX idx_pcr_code_user ON auth.promo_code_redemptions(promo_code_id, user_id);
-- Fast global count:   WHERE promo_code_id=$1
CREATE INDEX idx_pcr_code      ON auth.promo_code_redemptions(promo_code_id);

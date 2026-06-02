-- Scope the source_ref uniqueness constraint to Stripe rows only.
--
-- The original full-table UNIQUE(source_ref) would prevent multiple users from
-- redeeming the same promo code, because all promo-code subscriptions share the
-- same source_ref (the promo code UUID). A partial index scoped to
-- source = 'stripe' preserves the deduplication guarantee for concurrent Stripe
-- webhook deliveries while allowing any number of promo-code redemptions with
-- the same source_ref.
--
-- Drop the full constraint first (idempotent for fresh installs where it was
-- never created as a constraint vs. an index).
ALTER TABLE auth.subscriptions DROP CONSTRAINT IF EXISTS uq_subscriptions_source_ref;

CREATE UNIQUE INDEX uq_subscriptions_source_ref
  ON auth.subscriptions (source_ref)
  WHERE source = 'stripe';

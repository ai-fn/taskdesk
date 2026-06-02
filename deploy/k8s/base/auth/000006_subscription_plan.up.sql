ALTER TABLE auth.users
  ADD COLUMN plan              TEXT NOT NULL DEFAULT 'free',
  ADD COLUMN stripe_customer_id TEXT;

ALTER TABLE auth.users
  DROP COLUMN IF EXISTS plan,
  DROP COLUMN IF EXISTS stripe_customer_id;

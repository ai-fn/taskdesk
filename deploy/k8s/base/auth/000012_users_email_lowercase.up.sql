-- Runs after 000011 so the lower(email) probes below use its index.
-- Emails are matched case-insensitively from now on. Lowercase every address that cannot
-- collide with another account; case-duplicates are left for an operator to merge:
--   SELECT lower(email), array_agg(id) FROM auth.users GROUP BY 1 HAVING count(*) > 1;
UPDATE auth.users u
SET email = lower(email)
WHERE email <> lower(email)
  AND NOT EXISTS (SELECT 1 FROM auth.users o WHERE o.id <> u.id AND lower(o.email) = lower(u.email));

-- Superseded by 000010_profile_zero_to_one_retry; intentionally a no-op.
--
-- This migration originally folded the fake profile 0 into profile 1, but it
-- compared ids across tables directly and failed with "Illegal mix of
-- collations" on legacy databases whose tables use different collations.
-- Older builds then forced the dirty version clean and moved on, so on those
-- databases it never ran. Fixing it in place would not help them, so the
-- corrected, re-runnable version is 000010, which every database runs: it is a
-- no-op where this migration already succeeded.
SELECT 1;

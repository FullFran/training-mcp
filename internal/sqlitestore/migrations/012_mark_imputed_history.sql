-- One-off data correction, confirmed by the user on 2026-10-30. These sets were
-- entered on the night of 2026-10-29 with no RPE available, so 8 was filled in:
-- a backfill dated January and February 2025, and that day's session. They stay
-- in the totals at one SI unit each; only the mark changes. Matching on insertion
-- time as well as training date leaves anything logged by hand on those dates
-- alone, and it matches nothing on a fresh database.
UPDATE sets SET rpe_imputed = 1
WHERE rpe = 8
  AND created_at >= '2026-10-29' AND created_at < '2026-10-30'
  AND session_id IN (
    SELECT id FROM sessions
    WHERE date BETWEEN '2025-01-27' AND '2025-02-24' OR date = '2026-10-29'
  );

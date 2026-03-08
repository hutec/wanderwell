-- =============================================================================
-- Test harness for GetRouteNonOverlappingKm query development
-- =============================================================================
-- Usage: cat db/test_non_overlap.sql | psql <conn_string>
--
-- Key findings:
--   OLD approach (segment hash / ST_SnapToGrid):
--     - Ran out of PostgreSQL temp disk space after 47s even on the shortest route
--     - Fundamentally O(N²): builds and joins segment strings for all route pairs
--
--   NEW approach (point sampling + ST_DWithin with GIST index):
--     - Densifies target route to one point per 20m
--     - Checks each point against other routes via ST_DWithin(pt, o.geom, 0.0001)
--       where 0.0001 degrees ≈ 11m (chosen for 10m GPS accuracy tolerance)
--     - Uses the route_geom_idx GIST spatial index → O(P × log R) not O(P × R)
--     - Results on user 41340942 (2988 routes):
--         1.8km  "Mittagspaziergang"              → 0.000km unique  in   20ms
--         7.9km  "2020-06-27 10:46:11" (isolated) → 7.943km unique  in   29ms ✓
--       109.7km  "From Dusk Till Dawn: Radelthon" → 14.249km unique in  8.4s
--       202.5km  "Kaunertal++ - D7"               → 141.350km unique in 3.9s
--       473.3km  "München-Bodensee-Karlsruhe"     → 317.351km unique in 7.5s
--
-- Test routes (user 41340942):
--   3678669420   - isolated ~7.9km  (zero spatial neighbors; should return full km)
--   13369490241  - short    ~1.8km  "Mittagspaziergang" (busy area, expect ~0)
--   7405837829   - medium   ~110km  "From Dusk Till Dawn: Radelthon"
--   9769970890   - large    ~202km  "Kaunertal++ - D7"
--   2397968428   - huge     ~473km  "München-Bodensee-Karlsruhe"
-- =============================================================================

\set ON_ERROR_STOP on
\timing on

\echo ''
\echo '=== DATABASE STATS ==='
SELECT COUNT(*) AS total_routes,
       COUNT(DISTINCT user_id) AS users,
       COUNT(*) FILTER (WHERE geom IS NOT NULL) AS routes_with_geom
FROM route;

\echo ''
\echo '=== TEST ROUTES ==='
SELECT id, name, ST_NPoints(geom) AS pts,
       round((ST_Length(geom::geography)/1000)::numeric, 2) AS km
FROM route
WHERE id IN (3678669420, 13369490241, 7405837829, 9769970890, 2397968428)
ORDER BY km;

-- =============================================================================
-- FINAL QUERY (matches db/query.sql GetRouteNonOverlappingKm)
-- =============================================================================

\echo ''
\echo '=== SANITY: isolated route 3678669420 (~7.9km, zero neighbors) — expect ~7.943km ==='
WITH target AS (
    SELECT geom, user_id
    FROM route
    WHERE route.id = 3678669420 AND geom IS NOT NULL
),
densified AS (
    SELECT ST_Segmentize(t.geom::geography, 20)::geometry AS dgeom, t.user_id FROM target t
),
pts AS (
    SELECT (dp).path[1] AS n, (dp).geom AS pt, d.user_id
    FROM densified d CROSS JOIN LATERAL ST_DumpPoints(d.dgeom) dp
),
pt_covered AS (
    SELECT p.n, p.pt,
           EXISTS (
               SELECT 1 FROM route o
               WHERE o.user_id = p.user_id AND o.id <> 3678669420
                 AND o.geom IS NOT NULL AND ST_DWithin(p.pt, o.geom, 0.0001)
           ) AS covered
    FROM pts p
),
segs AS (
    SELECT ST_MakeLine(a.pt, b.pt) AS seg, NOT (a.covered AND b.covered) AS is_unique
    FROM pt_covered a JOIN pt_covered b ON b.n = a.n + 1
)
SELECT COALESCE(SUM(ST_Length(seg::geography)) FILTER (WHERE is_unique), 0)::float8 / 1000.0 AS non_overlapping_km,
       (SELECT ST_Length(geom::geography)/1000 FROM route WHERE id = 3678669420) AS total_km
FROM segs;

\echo ''
\echo '=== SHORT BUSY: route 13369490241 (~1.8km) — expect 0.000km (entirely covered) ==='
WITH target AS (
    SELECT geom, user_id FROM route WHERE route.id = 13369490241 AND geom IS NOT NULL
),
densified AS (SELECT ST_Segmentize(t.geom::geography, 20)::geometry AS dgeom, t.user_id FROM target t),
pts AS (SELECT (dp).path[1] AS n, (dp).geom AS pt, d.user_id FROM densified d CROSS JOIN LATERAL ST_DumpPoints(d.dgeom) dp),
pt_covered AS (
    SELECT p.n, p.pt,
           EXISTS (SELECT 1 FROM route o WHERE o.user_id = p.user_id AND o.id <> 13369490241
                     AND o.geom IS NOT NULL AND ST_DWithin(p.pt, o.geom, 0.0001)) AS covered FROM pts p
),
segs AS (SELECT ST_MakeLine(a.pt, b.pt) AS seg, NOT (a.covered AND b.covered) AS is_unique
         FROM pt_covered a JOIN pt_covered b ON b.n = a.n + 1)
SELECT COALESCE(SUM(ST_Length(seg::geography)) FILTER (WHERE is_unique), 0)::float8 / 1000.0 AS non_overlapping_km,
       (SELECT ST_Length(geom::geography)/1000 FROM route WHERE id = 13369490241) AS total_km FROM segs;

\echo ''
\echo '=== MEDIUM: route 7405837829 (~110km) ==='
WITH target AS (
    SELECT geom, user_id FROM route WHERE route.id = 7405837829 AND geom IS NOT NULL
),
densified AS (SELECT ST_Segmentize(t.geom::geography, 20)::geometry AS dgeom, t.user_id FROM target t),
pts AS (SELECT (dp).path[1] AS n, (dp).geom AS pt, d.user_id FROM densified d CROSS JOIN LATERAL ST_DumpPoints(d.dgeom) dp),
pt_covered AS (
    SELECT p.n, p.pt,
           EXISTS (SELECT 1 FROM route o WHERE o.user_id = p.user_id AND o.id <> 7405837829
                     AND o.geom IS NOT NULL AND ST_DWithin(p.pt, o.geom, 0.0001)) AS covered FROM pts p
),
segs AS (SELECT ST_MakeLine(a.pt, b.pt) AS seg, NOT (a.covered AND b.covered) AS is_unique
         FROM pt_covered a JOIN pt_covered b ON b.n = a.n + 1)
SELECT COALESCE(SUM(ST_Length(seg::geography)) FILTER (WHERE is_unique), 0)::float8 / 1000.0 AS non_overlapping_km,
       (SELECT ST_Length(geom::geography)/1000 FROM route WHERE id = 7405837829) AS total_km FROM segs;


\set ON_ERROR_STOP on
\timing on

\echo ''
\echo '=== DATABASE STATS ==='
SELECT COUNT(*) AS total_routes,
       COUNT(DISTINCT user_id) AS users,
       COUNT(*) FILTER (WHERE geom IS NOT NULL) AS routes_with_geom
FROM route;

\echo ''
\echo '=== TEST ROUTES ==='
SELECT id, name, ST_NPoints(geom) AS pts,
       round((ST_Length(geom::geography)/1000)::numeric, 2) AS km
FROM route
WHERE id IN (7405837829, 9769970890, 13369490241, 2397968428)
ORDER BY km;

-- =============================================================================
-- APPROACH A: Current segment-string-hash approach (baseline for comparison)
-- =============================================================================

\echo ''
\echo '=== APPROACH A: Current query (segment hash) on short route 13369490241 ==='
EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
WITH target AS (
    SELECT r.user_id, r.geom
    FROM route r
    WHERE r.id = 13369490241
      AND r.geom IS NOT NULL
),
target_pts AS (
    SELECT (dp).path[1] AS n, (dp).geom AS pt, t.user_id
    FROM target t
    CROSS JOIN LATERAL ST_DumpPoints(t.geom) AS dp
),
target_segs AS (
    SELECT tp1.user_id, ST_MakeLine(tp1.pt, tp2.pt) AS seg
    FROM target_pts tp1
    JOIN target_pts tp2 ON tp2.n = tp1.n + 1
),
target_keys AS (
    SELECT DISTINCT
        CASE
            WHEN ST_AsText(ST_StartPoint(sg)) <= ST_AsText(ST_EndPoint(sg))
                THEN ST_AsText(ST_SnapToGrid(sg, 0.00005))
            ELSE ST_AsText(ST_Reverse(ST_SnapToGrid(sg, 0.00005)))
        END AS seg_key,
        ST_Length(sg::geography) AS seg_len_m
    FROM (SELECT ST_LineMerge(seg) AS sg FROM target_segs) x
    WHERE GeometryType(sg) = 'LINESTRING' AND ST_NPoints(sg) = 2
),
other_routes AS (
    SELECT o.geom
    FROM route o
    JOIN target t ON o.user_id = t.user_id
    WHERE o.id <> 13369490241
      AND o.geom IS NOT NULL
      AND o.geom && ST_Expand((SELECT geom FROM route WHERE id = 13369490241), 0.01)
),
other_pts AS (
    SELECT o.geom, (dp).path[1] AS n, (dp).geom AS pt
    FROM other_routes o
    CROSS JOIN LATERAL ST_DumpPoints(o.geom) AS dp
),
other_segs AS (
    SELECT ST_MakeLine(op1.pt, op2.pt) AS seg
    FROM other_pts op1
    JOIN other_pts op2 ON op2.geom = op1.geom AND op2.n = op1.n + 1
),
other_keys AS (
    SELECT DISTINCT
        CASE
            WHEN ST_AsText(ST_StartPoint(sg)) <= ST_AsText(ST_EndPoint(sg))
                THEN ST_AsText(ST_SnapToGrid(sg, 0.00005))
            ELSE ST_AsText(ST_Reverse(ST_SnapToGrid(sg, 0.00005)))
        END AS seg_key
    FROM (SELECT ST_LineMerge(seg) AS sg FROM other_segs) x
    WHERE GeometryType(sg) = 'LINESTRING' AND ST_NPoints(sg) = 2
)
SELECT COALESCE(SUM(tk.seg_len_m) FILTER (WHERE ok.seg_key IS NULL), 0.0) / 1000.0 AS non_overlapping_km
FROM target_keys tk
LEFT JOIN other_keys ok ON ok.seg_key = tk.seg_key;

-- =============================================================================
-- APPROACH B: Buffer + ST_Difference (new approach)
-- =============================================================================
-- Concept:
--   1. Get target route geometry
--   2. Find all other routes from same user that touch the target's bounding box
--   3. Buffer them by 10m and union
--   4. Subtract buffered area from target route
--   5. Measure remaining length

\echo ''
\echo '=== APPROACH B: Buffer+Difference on short route 13369490241 ==='
EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
WITH target AS (
    SELECT geom, user_id
    FROM route
    WHERE id = 13369490241 AND geom IS NOT NULL
),
other_buffered AS (
    SELECT ST_Union(
        ST_Buffer(o.geom::geography, 10)::geometry
    ) AS buf
    FROM route o, target t
    WHERE o.user_id = t.user_id
      AND o.id <> 13369490241
      AND o.geom IS NOT NULL
      AND o.geom && ST_Expand(t.geom, 0.001)
),
diff AS (
    SELECT
        CASE
            WHEN ob.buf IS NULL THEN t.geom
            ELSE ST_Difference(t.geom, ob.buf)
        END AS unique_geom
    FROM target t
    LEFT JOIN other_buffered ob ON true
)
SELECT COALESCE(ST_Length(unique_geom::geography) / 1000.0, 0.0) AS non_overlapping_km
FROM diff;

\echo ''
\echo '=== Result comparison: Approach A vs B for short route ==='
\echo '--- Approach A result ---'
WITH target AS (
    SELECT r.user_id, r.geom
    FROM route r
    WHERE r.id = 13369490241
      AND r.geom IS NOT NULL
),
target_pts AS (
    SELECT (dp).path[1] AS n, (dp).geom AS pt, t.user_id
    FROM target t
    CROSS JOIN LATERAL ST_DumpPoints(t.geom) AS dp
),
target_segs AS (
    SELECT tp1.user_id, ST_MakeLine(tp1.pt, tp2.pt) AS seg
    FROM target_pts tp1
    JOIN target_pts tp2 ON tp2.n = tp1.n + 1
),
target_keys AS (
    SELECT DISTINCT
        CASE
            WHEN ST_AsText(ST_StartPoint(sg)) <= ST_AsText(ST_EndPoint(sg))
                THEN ST_AsText(ST_SnapToGrid(sg, 0.00005))
            ELSE ST_AsText(ST_Reverse(ST_SnapToGrid(sg, 0.00005)))
        END AS seg_key,
        ST_Length(sg::geography) AS seg_len_m
    FROM (SELECT ST_LineMerge(seg) AS sg FROM target_segs) x
    WHERE GeometryType(sg) = 'LINESTRING' AND ST_NPoints(sg) = 2
),
other_routes AS (
    SELECT o.geom
    FROM route o
    JOIN target t ON o.user_id = t.user_id
    WHERE o.id <> 13369490241
      AND o.geom IS NOT NULL
      AND o.geom && ST_Expand((SELECT geom FROM route WHERE id = 13369490241), 0.01)
),
other_pts AS (
    SELECT o.geom, (dp).path[1] AS n, (dp).geom AS pt
    FROM other_routes o
    CROSS JOIN LATERAL ST_DumpPoints(o.geom) AS dp
),
other_segs AS (
    SELECT ST_MakeLine(op1.pt, op2.pt) AS seg
    FROM other_pts op1
    JOIN other_pts op2 ON op2.geom = op1.geom AND op2.n = op1.n + 1
),
other_keys AS (
    SELECT DISTINCT
        CASE
            WHEN ST_AsText(ST_StartPoint(sg)) <= ST_AsText(ST_EndPoint(sg))
                THEN ST_AsText(ST_SnapToGrid(sg, 0.00005))
            ELSE ST_AsText(ST_Reverse(ST_SnapToGrid(sg, 0.00005)))
        END AS seg_key
    FROM (SELECT ST_LineMerge(seg) AS sg FROM other_segs) x
    WHERE GeometryType(sg) = 'LINESTRING' AND ST_NPoints(sg) = 2
)
SELECT round(COALESCE(SUM(tk.seg_len_m) FILTER (WHERE ok.seg_key IS NULL), 0.0) / 1000.0, 3) AS approach_a_km
FROM target_keys tk
LEFT JOIN other_keys ok ON ok.seg_key = tk.seg_key;

\echo '--- Approach B result ---'
WITH target AS (
    SELECT geom, user_id FROM route WHERE id = 13369490241 AND geom IS NOT NULL
),
other_buffered AS (
    SELECT ST_Union(ST_Buffer(o.geom::geography, 10)::geometry) AS buf
    FROM route o, target t
    WHERE o.user_id = t.user_id AND o.id <> 13369490241 AND o.geom IS NOT NULL
      AND o.geom && ST_Expand(t.geom, 0.001)
),
diff AS (
    SELECT CASE WHEN ob.buf IS NULL THEN t.geom ELSE ST_Difference(t.geom, ob.buf) END AS unique_geom
    FROM target t LEFT JOIN other_buffered ob ON true
)
SELECT round(COALESCE(ST_Length(unique_geom::geography) / 1000.0, 0.0)::numeric, 3) AS approach_b_km
FROM diff;

-- Full route km for reference
\echo '--- Full route km (reference) ---'
SELECT round((ST_Length(geom::geography)/1000)::numeric, 3) AS total_km
FROM route WHERE id = 13369490241;

-- =============================================================================
-- APPROACH B on medium route (110km)
-- =============================================================================
\echo ''
\echo '=== APPROACH B: medium route 7405837829 (~110km) ==='
\timing on
WITH target AS (
    SELECT geom, user_id FROM route WHERE id = 7405837829 AND geom IS NOT NULL
),
other_buffered AS (
    SELECT ST_Union(ST_Buffer(o.geom::geography, 10)::geometry) AS buf
    FROM route o, target t
    WHERE o.user_id = t.user_id AND o.id <> 7405837829 AND o.geom IS NOT NULL
      AND o.geom && ST_Expand(t.geom, 0.001)
),
diff AS (
    SELECT CASE WHEN ob.buf IS NULL THEN t.geom ELSE ST_Difference(t.geom, ob.buf) END AS unique_geom
    FROM target t LEFT JOIN other_buffered ob ON true
)
SELECT round(COALESCE(ST_Length(unique_geom::geography) / 1000.0, 0.0)::numeric, 3) AS non_overlapping_km,
       round((SELECT ST_Length(geom::geography)/1000 FROM route WHERE id = 7405837829)::numeric, 3) AS total_km;

-- =============================================================================
-- APPROACH B on large route (202km)
-- =============================================================================
\echo ''
\echo '=== APPROACH B: large route 9769970890 (~202km) ==='
WITH target AS (
    SELECT geom, user_id FROM route WHERE id = 9769970890 AND geom IS NOT NULL
),
other_buffered AS (
    SELECT ST_Union(ST_Buffer(o.geom::geography, 10)::geometry) AS buf
    FROM route o, target t
    WHERE o.user_id = t.user_id AND o.id <> 9769970890 AND o.geom IS NOT NULL
      AND o.geom && ST_Expand(t.geom, 0.001)
),
diff AS (
    SELECT CASE WHEN ob.buf IS NULL THEN t.geom ELSE ST_Difference(t.geom, ob.buf) END AS unique_geom
    FROM target t LEFT JOIN other_buffered ob ON true
)
SELECT round(COALESCE(ST_Length(unique_geom::geography) / 1000.0, 0.0)::numeric, 3) AS non_overlapping_km,
       round((SELECT ST_Length(geom::geography)/1000 FROM route WHERE id = 9769970890)::numeric, 3) AS total_km;

-- =============================================================================
-- SANITY CHECK: Route ridden only once (unique segments) should return ~full km
-- Find a recent route with no spatial overlap candidate
-- =============================================================================
\echo ''
\echo '=== SANITY: Finding a candidate "unique" route (few spatial neighbors) ==='
SELECT r.id, r.name,
       round((ST_Length(r.geom::geography)/1000)::numeric, 2) AS km,
       COUNT(o.id) AS nearby_routes
FROM route r
LEFT JOIN route o ON o.user_id = r.user_id
    AND o.id <> r.id
    AND o.geom && ST_Expand(r.geom, 0.001)
WHERE r.user_id = 41340942 AND r.geom IS NOT NULL
GROUP BY r.id, r.name, r.geom
ORDER BY nearby_routes ASC
LIMIT 10;

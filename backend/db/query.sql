-- name: GetAthleteTokens :one
SELECT id, expires_at, refresh_token, access_token
FROM athlete
WHERE id = $1;

-- name: UpdateAthleteTokens :exec
UPDATE athlete
SET access_token = $1,
    expires_at   = $2
WHERE id = $3;

-- name: UpsertAthlete :exec
INSERT INTO athlete (id, firstname, lastname, access_token, refresh_token, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (id) DO UPDATE SET
    firstname     = EXCLUDED.firstname,
    lastname      = EXCLUDED.lastname,
    access_token  = EXCLUDED.access_token,
    refresh_token = EXCLUDED.refresh_token,
    expires_at    = EXCLUDED.expires_at;

-- name: GetAthlete :one
SELECT id, firstname, lastname
FROM athlete
WHERE id = $1;

-- name: ListAthleteIDs :many
SELECT id
FROM athlete;

-- name: GetWriteUniqueDistancePreference :one
SELECT COALESCE(
    (
        SELECT write_unique_distance
        FROM user_preferences
        WHERE user_id = $1
    ),
    FALSE
)::boolean AS write_unique_distance;

-- name: RouteExists :one
SELECT COUNT(*) > 0
FROM route
WHERE id = $1;

-- name: UpsertRoute :exec
INSERT INTO route (id, user_id, start_date, name, elapsed_time, moving_time, distance, average_speed, elevation, bounds, geom)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, ST_GeomFromText($11, 4326))
ON CONFLICT (id) DO UPDATE SET
    user_id       = EXCLUDED.user_id,
    start_date    = EXCLUDED.start_date,
    name          = EXCLUDED.name,
    elapsed_time  = EXCLUDED.elapsed_time,
    moving_time   = EXCLUDED.moving_time,
    distance      = EXCLUDED.distance,
    average_speed = EXCLUDED.average_speed,
    elevation     = EXCLUDED.elevation,
    bounds        = EXCLUDED.bounds,
    geom          = EXCLUDED.geom;


-- name: GetRouteName :one
SELECT name
FROM route
WHERE id = $1 AND user_id = $2;

-- name: UpdateRouteName :exec
UPDATE route
SET name = $1
WHERE id = $2 AND user_id = $3;

-- name: ListRoutesByUser :many
SELECT id, user_id, start_date, name, elapsed_time, moving_time, distance, average_speed, elevation, bounds
FROM route
WHERE user_id = $1
ORDER BY start_date DESC;

-- name: GetRouteUniqueDistanceMeters :one
-- Computes the meters of the route that don't come within 10m of any other route
-- from the same user. Uses a point-sampling approach (one point per 20m) with
-- geometry ST_DWithin so the GIST spatial index is used for each lookup.
-- The result is capped at the route's own Strava-reported distance (in metres)
-- so that a fully-unique route can never return a value larger than the route
-- itself (PostGIS measures the raw GPS polyline, which is slightly longer than
-- Strava's smoothed distance).
WITH target AS (
    SELECT geom, user_id
    FROM route
    WHERE route.id = $1
      AND geom IS NOT NULL
),
densified AS (
    -- Resample the route to one vertex every 20m for uniform coverage
    SELECT ST_Segmentize(t.geom::geography, 20)::geometry AS dgeom, t.user_id
    FROM target t
),
pts AS (
    SELECT (dp).path[1] AS n, (dp).geom AS pt, d.user_id
    FROM densified d
    CROSS JOIN LATERAL ST_DumpPoints(d.dgeom) dp
),
pt_covered AS (
    -- A point is "covered" if any other route from the same user passes within 10m.
    -- 0.0001 degrees ≈ 11m; using geometry (not geography) keeps the GIST index active.
    SELECT p.n, p.pt,
           EXISTS (
               SELECT 1
               FROM route o
               WHERE o.user_id = p.user_id
                 AND o.id <> $1
                 AND o.geom IS NOT NULL
                 AND ST_DWithin(p.pt, o.geom, 0.0001)
           ) AS covered
    FROM pts p
),
segs AS (
    -- A segment is unique if at least one of its endpoints is not covered
    SELECT ST_MakeLine(a.pt, b.pt) AS seg,
           NOT (a.covered AND b.covered) AS is_unique
    FROM pt_covered a
    JOIN pt_covered b ON b.n = a.n + 1
)
SELECT LEAST(
    COALESCE(SUM(ST_Length(seg::geography)) FILTER (WHERE is_unique), 0),
    (SELECT distance * 1000 FROM route WHERE route.id = $1)
)::double precision AS unique_distance_meters
FROM segs;

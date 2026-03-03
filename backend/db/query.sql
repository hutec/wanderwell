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

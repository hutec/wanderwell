CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE IF NOT EXISTS athlete (
    id            BIGINT PRIMARY KEY,
    firstname     TEXT,
    lastname      TEXT,
    expires_at    BIGINT,
    refresh_token TEXT,
    access_token  TEXT
);

CREATE TABLE IF NOT EXISTS route (
    id            BIGINT PRIMARY KEY,
    user_id       BIGINT NOT NULL,
    start_date    TIMESTAMPTZ NOT NULL,
    name          VARCHAR NOT NULL,
    elapsed_time  INTEGER NOT NULL,
    moving_time   INTEGER NOT NULL,
    distance      FLOAT NOT NULL,
    average_speed FLOAT NOT NULL,
    elevation     FLOAT NOT NULL,
    bounds        TEXT NOT NULL,
    sport_type    VARCHAR,
    geom          geometry(LineString, 4326),
    FOREIGN KEY (user_id) REFERENCES athlete(id)
);

CREATE TABLE IF NOT EXISTS user_preferences (
    user_id BIGINT PRIMARY KEY,
    write_unique_distance BOOLEAN NOT NULL DEFAULT FALSE,
    FOREIGN KEY (user_id) REFERENCES athlete(id)
);

-- Backfill user_preferences for existing athletes
INSERT INTO user_preferences (user_id)
SELECT id
FROM athlete
ON CONFLICT (user_id) DO NOTHING;

-- Ensure user_preferences entry exists for new athletes
CREATE OR REPLACE FUNCTION ensure_user_preferences()
RETURNS trigger AS $$
BEGIN
    INSERT INTO user_preferences (user_id)
    VALUES (NEW.id)
    ON CONFLICT (user_id) DO NOTHING;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create trigger to call the function after inserting a new athlete
DROP TRIGGER IF EXISTS athlete_user_preferences_defaults ON athlete;
CREATE TRIGGER athlete_user_preferences_defaults
AFTER INSERT ON athlete
FOR EACH ROW
EXECUTE FUNCTION ensure_user_preferences();

-- Create spatial index
CREATE INDEX IF NOT EXISTS route_geom_idx ON route USING GIST (geom);
CREATE INDEX IF NOT EXISTS route_user_id_id_idx ON route (user_id, id);

-- Create MVT function for user routes
CREATE OR REPLACE FUNCTION user_routes(z int, x int, y int, query_params json)
		RETURNS bytea AS $$
		DECLARE
		  mvt bytea;
		  uid bigint;
		BEGIN
		  uid := (query_params->>'user_id')::bigint;

		  SELECT INTO mvt ST_AsMVT(tile, 'user_routes', 4096, 'geom')
		  FROM (
		    SELECT
		      id,
		      name,
		      sport_type,
		      distance,
		      start_date,
		      ST_AsMVTGeom(
		        ST_Transform(geom, 3857),
		        ST_TileEnvelope(z, x, y),
		        4096, 64, true
		      ) AS geom
		    FROM route
		    WHERE user_id = uid AND geom && ST_Transform(ST_TileEnvelope(z, x, y), 4326)
		  ) tile;

		  RETURN mvt;
		END;
		$$ LANGUAGE plpgsql STABLE PARALLEL SAFE;

-- Create MVT function for VeloViewer-style "Explorer" tiles.
--
-- The Explorer view divides the world into the standard slippy-map grid at
-- zoom 14; a grid cell counts as "explored" once any of the user's routes
-- passes through it. This function returns, for the requested (z, x, y) tile,
-- the set of explored zoom-14 cells (as polygons) that fall within it.
--
-- The computation runs entirely in PostGIS and is bounded by the length of the
-- routes visible in the requested tile (not by zoom level): each visible route
-- is clipped to the tile, densified so no zoom-14 cell (~2.4 km wide) is
-- skipped, and every sample point is mapped to its zoom-14 cell coordinates.
-- Results are cached per user by Vinyl Cache (varnish) via the user_id param.
CREATE OR REPLACE FUNCTION user_explorer_tiles(z int, x int, y int, query_params json)
		RETURNS bytea AS $$
		DECLARE
		  mvt bytea;
		  uid bigint;
		  grid_z constant int := 14;
		  world constant double precision := 20037508.342789244;
		  n double precision := (1 << grid_z);
		  step_m constant double precision := 500; -- densify step (< zoom-14 cell width) so cells are not skipped
		  env geometry := ST_TileEnvelope(z, x, y);
		  env4326 geometry := ST_Transform(env, 4326);
		BEGIN
		  uid := (query_params->>'user_id')::bigint;

		  SELECT INTO mvt ST_AsMVT(tile, 'user_explorer_tiles', 4096, 'geom')
		  FROM (
		    SELECT
		      tx AS x,
		      ty AS y,
		      ST_AsMVTGeom(
		        ST_TileEnvelope(grid_z, tx, ty),
		        env, 4096, 0, true
		      ) AS geom
		    FROM (
		      -- Distinct zoom-14 cells touched by the user's routes within this tile.
		      SELECT DISTINCT
		        floor((ST_X(pt) + world) / (2 * world) * n)::int AS tx,
		        floor((world - ST_Y(pt)) / (2 * world) * n)::int AS ty
		      FROM (
		        SELECT (ST_DumpPoints(
		                  ST_Segmentize(
		                    ST_Transform(ST_Intersection(r.geom, env4326), 3857),
		                    step_m))).geom AS pt
		        FROM route r
		        WHERE r.user_id = uid
		          AND r.geom IS NOT NULL
		          AND r.geom && env4326
		      ) p
		    ) tiles
		  ) tile;

		  RETURN mvt;
		END;
		$$ LANGUAGE plpgsql STABLE PARALLEL SAFE;

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

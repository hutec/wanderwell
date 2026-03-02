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

CREATE INDEX IF NOT EXISTS route_geom_idx ON route USING GIST (geom);

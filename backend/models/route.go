package models

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/twpayne/go-polyline"
)

type Route struct {
	ID           int64     `json:"id"`
	UserID       int64     `json:"user_id"`
	StartDate    time.Time `json:"start_date"`
	Name         string    `json:"name"`
	ElapsedTime  int32     `json:"elapsed_time"`
	MovingTime   int32     `json:"moving_time"`
	Distance     float32   `json:"distance"`
	AverageSpeed float32   `json:"average_speed"`
	Route        *string   `json:"route,omitempty"` // Raw polyline string
	Elevation    float32   `json:"elevation"`
	Bounds       string    `json:"bounds"`
}

func (r *Route) Exists(db *sql.DB) (bool, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM route WHERE id = $1", r.ID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *Route) Add(db *sql.DB) error {
	var wkt *string
	if r.Route != nil && *r.Route != "" {
		coords, _, decodeErr := polyline.DecodeCoords([]byte(*r.Route))
		if decodeErr != nil {
			slog.Error("Error decoding polyline for route", "route_id", r.ID, "err", decodeErr)
		} else {
			wktValue := CoordsToWKT(coords)
			wkt = &wktValue
		}
	}

	_, err := db.Exec("INSERT INTO route (id, user_id, start_date, name, elapsed_time, moving_time, distance, average_speed, elevation, bounds, geom) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, ST_GeomFromText($11, 4326))",
		r.ID, r.UserID, r.StartDate, r.Name, r.ElapsedTime, r.MovingTime, r.Distance, r.AverageSpeed, r.Elevation, r.Bounds, wkt)
	return err
}

func (r *Route) Update(db *sql.DB) error {
	var wkt *string
	if r.Route != nil && *r.Route != "" {
		coords, _, decodeErr := polyline.DecodeCoords([]byte(*r.Route))
		if decodeErr != nil {
			slog.Error("Error decoding polyline for route", "route_id", r.ID, "err", decodeErr)
		} else {
			wktValue := CoordsToWKT(coords)
			wkt = &wktValue
		}
	}

	_, err := db.Exec("UPDATE route SET user_id = $1, start_date = $2, name = $3, elapsed_time = $4, moving_time = $5, distance = $6, average_speed = $7, elevation = $8, bounds = $9, geom = ST_GeomFromText($11, 4326) WHERE id = $10",
		r.UserID, r.StartDate, r.Name, r.ElapsedTime, r.MovingTime, r.Distance, r.AverageSpeed, r.Elevation, r.Bounds, r.ID, wkt)
	return err
}

// CoordsToWKT converts a slice of coordinates to a WKT (Well-Known Text) representation of a LINESTRING
func CoordsToWKT(coords [][]float64) string {
	if len(coords) == 0 {
		return "LINESTRING EMPTY"
	}

	wkt := "LINESTRING("
	for i, coord := range coords {
		if i > 0 {
			wkt += ", "
		}
		// WKT format is: longitude latitude (note: reversed from lat,lng)
		wkt += fmt.Sprintf("%f %f", coord[1], coord[0])
	}
	wkt += ")"
	return wkt
}

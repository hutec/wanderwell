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
	Route        string    `json:"route"` // Raw polyline string
	Elevation    float32   `json:"elevation"`
	Bounds       string    `json:"bounds"`
	SportType    string    `json:sport_type`
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

	coords, _, decodeErr := polyline.DecodeCoords([]byte(r.Route))
	if decodeErr != nil {
		slog.Error("Error decoding polyline for route %d: %v", r.ID, decodeErr)
	}
	// Convert to WKT LineString format
	wkt := coordsToWKT(coords)

	_, err := db.Exec("INSERT INTO route (id, user_id, start_date, name, elapsed_time, moving_time, distance, average_speed, route, elevation, bounds, sport_type, geom) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, ST_GeomFromText($13, 4326))",
		r.ID, r.UserID, r.StartDate, r.Name, r.ElapsedTime, r.MovingTime, r.Distance, r.AverageSpeed, r.Route, r.Elevation, r.Bounds, r.SportType, wkt)
	return err
}

func (r *Route) Update(db *sql.DB) error {
	coords, _, decodeErr := polyline.DecodeCoords([]byte(r.Route))
	if decodeErr != nil {
		slog.Error("Error decoding polyline for route %d: %v", r.ID, decodeErr)
	}
	// Convert to WKT LineString format
	wkt := coordsToWKT(coords)

	_, err := db.Exec("UPDATE route SET user_id = $1, start_date = $2, name = $3, elapsed_time = $4, moving_time = $5, distance = $6, average_speed = $7, route = $8, elevation = $9, bounds = $10, sport_type = $11, geom = ST_GeomFromText($13, 4326) WHERE id = $12",
		r.UserID, r.StartDate, r.Name, r.ElapsedTime, r.MovingTime, r.Distance, r.AverageSpeed, r.Route, r.Elevation, r.Bounds, r.SportType, r.ID, wkt)
	return err
}

// coordsToWKT converts a slice of coordinates to a WKT (Well-Known Text) representation of a LINESTRING
func coordsToWKT(coords [][]float64) string {
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

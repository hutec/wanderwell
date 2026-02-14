package models

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/twpayne/go-polyline"
)

type Route struct {
	ID           int64       `json:"id"`
	UserID       int64       `json:"user_id"`
	StartDate    time.Time   `json:"start_date"`
	Name         string      `json:"name"`
	ElapsedTime  int32       `json:"elapsed_time"`
	MovingTime   int32       `json:"moving_time"`
	Distance     float32     `json:"distance"`
	AverageSpeed float32     `json:"average_speed"`
	Route        string      `json:"-"` // Raw polyline string
	Elevation    float32     `json:"elevation"`
	Bounds       string      `json:"bounds"`
	Coordinates  [][]float64 `json:"route"` // Decoded coordinates
}

// MarshalJSON implements custom JSON marshaling to decode polyline
func (r *Route) MarshalJSON() ([]byte, error) {
	// type Alias Route

	// Create a copy to modify
	routeCopy := *r

	// Decode the polyline if it exists
	if r.Route != "" {
		coords, _, err := polyline.DecodeCoords([]byte(r.Route))
		if err != nil {
			// If decoding fails, return empty coordinates array
			routeCopy.Coordinates = [][]float64{}
		} else {
			routeCopy.Coordinates = coords
		}
	} else {
		routeCopy.Coordinates = [][]float64{}
	}

	return json.Marshal((Route)(routeCopy))
}

func (r *Route) Exists(db *sql.DB) (bool, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM route WHERE id = ?", r.ID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *Route) Add(db *sql.DB) error {
	_, err := db.Exec("INSERT INTO route (id, user_id, start_date, name, elapsed_time, moving_time, distance, average_speed, route, elevation, bounds) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.ID, r.UserID, r.StartDate, r.Name, r.ElapsedTime, r.MovingTime, r.Distance, r.AverageSpeed, r.Route, r.Elevation, r.Bounds)
	return err
}

func (r *Route) Update(db *sql.DB) error {
	_, err := db.Exec("UPDATE route SET user_id = ?, start_date = ?, name = ?, elapsed_time = ?, moving_time = ?, distance = ?, average_speed = ?, route = ?, elevation = ?, bounds = ? WHERE id = ?",
		r.UserID, r.StartDate, r.Name, r.ElapsedTime, r.MovingTime, r.Distance, r.AverageSpeed, r.Route, r.Elevation, r.Bounds, r.ID)
	return err
}

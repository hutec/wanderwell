package models

import (
	"fmt"
)

// CoordsToWKT converts a slice of coordinates to a WKT (Well-Known Text) representation of a LINESTRING
// TODO: Can be optimized
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

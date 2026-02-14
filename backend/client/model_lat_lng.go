package swagger

import (
	"encoding/json"
	"fmt"
)

// A pair of latitude/longitude coordinates, represented as an array of 2 floating point numbers.
type LatLng struct {
	// The first element in the array represents the latitude of the coordinate.
	Lat float32 `json:"lat,omitempty"`
	// The second element in the array represents the longitude of the coordinate.
	Lng float32 `json:"lng,omitempty"`
}

func (ll *LatLng) UnmarshalJSON(data []byte) error {
	// Handle null values
	if string(data) == "null" {
		ll.Lat = 0
		ll.Lng = 0
		return nil
	}

	// Try to unmarshal as array first
	var coords []float32
	if err := json.Unmarshal(data, &coords); err == nil {
		// Handle empty array
		if len(coords) == 0 {
			ll.Lat = 0
			ll.Lng = 0
			return nil
		}

		if len(coords) >= 2 {
			ll.Lat = coords[0]
			ll.Lng = coords[1]
			return nil
		}
		fmt.Printf("Debug: coords array has %d elements: %v\n", len(coords), coords)
		return fmt.Errorf("coordinate array must have at least 2 elements")
	}

	// Fallback to object format
	type Alias LatLng
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(ll),
	}
	return json.Unmarshal(data, &aux)
}

// MarshalJSON implements custom JSON marshaling for LatLng
func (ll LatLng) MarshalJSON() ([]byte, error) {
	return json.Marshal([]float32{ll.Lat, ll.Lng})
}

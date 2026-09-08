package api

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"time"
)

const tileserverStyleBundlePath = "/internal/tileserver/style-bundle.tar.gz"

// getTileserverStyleBundle produces the complete, startup-time TileServer GL
// configuration. TileServer GL loads styles from disk, so its container fetches
// this archive before it starts rather than trying to resolve a style per tile.
func (s *Server) getTileserverStyleBundle(w http.ResponseWriter, r *http.Request) {
	athleteIDs, err := s.queries.ListAthleteIDs(r.Context())
	if err != nil {
		slog.Error("Failed to list athletes for TileServer GL style bundle", "error", err)
		http.Error(w, "Failed to generate TileServer GL styles", http.StatusInternalServerError)
		return
	}

	config := tileserverConfig{
		Options: tileserverOptions{Paths: tileserverPaths{Styles: "styles"}},
		Styles:  make(map[string]tileserverStyleConfig, len(athleteIDs)),
		Data:    map[string]any{},
	}
	styles := make(map[string]tileserverExplorerStyle, len(athleteIDs))
	for _, athleteID := range athleteIDs {
		name := fmt.Sprintf("explorer-%d", athleteID)
		config.Styles[name] = tileserverStyleConfig{
			Style:    name + ".json",
			TileJSON: explorerTileJSON,
		}
		styles[name] = newExplorerStyle(athleteID)
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="tileserver-styles.tar.gz"`)
	w.Header().Set("Cache-Control", "no-store")

	gz := gzip.NewWriter(w)
	defer gz.Close()
	tarWriter := tar.NewWriter(gz)
	defer tarWriter.Close()

	if err := writeBundleJSON(tarWriter, "config.json", config); err != nil {
		slog.Error("Failed to write TileServer GL configuration bundle", "error", err)
		return
	}
	for name, style := range styles {
		if err := writeBundleJSON(tarWriter, path.Join("styles", name+".json"), style); err != nil {
			slog.Error("Failed to write TileServer GL style bundle", "style", name, "error", err)
			return
		}
	}
}

func writeBundleJSON(tw *tar.Writer, filename string, value any) error {
	contents, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{
		Name:    filename,
		Mode:    0o644,
		Size:    int64(len(contents)),
		ModTime: time.Now(),
	}); err != nil {
		return err
	}
	_, err = tw.Write(contents)
	return err
}

var explorerTileJSON = tileserverTileJSON{
	Type:   "overlay",
	Format: "png",
	Bounds: [4]float64{-180, -85.05112877980659, 180, 85.05112877980659},
}

type tileserverConfig struct {
	Options tileserverOptions                `json:"options"`
	Styles  map[string]tileserverStyleConfig `json:"styles"`
	Data    map[string]any                   `json:"data"`
}

type tileserverOptions struct {
	Paths tileserverPaths `json:"paths"`
}

type tileserverPaths struct {
	Root    string `json:"root"`
	Styles  string `json:"styles"`
	Fonts   string `json:"fonts"`
	Sprites string `json:"sprites"`
	MBTiles string `json:"mbtiles"`
}

type tileserverStyleConfig struct {
	Style    string             `json:"style"`
	TileJSON tileserverTileJSON `json:"tilejson"`
}

type tileserverTileJSON struct {
	Type   string     `json:"type"`
	Format string     `json:"format"`
	Bounds [4]float64 `json:"bounds"`
}

type tileserverExplorerStyle struct {
	Version int                               `json:"version"`
	Name    string                            `json:"name"`
	Center  [2]float64                        `json:"center"`
	Zoom    int                               `json:"zoom"`
	Sources map[string]tileserverVectorSource `json:"sources"`
	Layers  []tileserverFillLayer             `json:"layers"`
}

type tileserverVectorSource struct {
	Type    string   `json:"type"`
	Tiles   []string `json:"tiles"`
	MinZoom int      `json:"minzoom"`
	MaxZoom int      `json:"maxzoom"`
}

type tileserverFillLayer struct {
	ID          string                 `json:"id"`
	Type        string                 `json:"type"`
	Source      string                 `json:"source"`
	SourceLayer string                 `json:"source-layer"`
	Paint       map[string]interface{} `json:"paint"`
}

func newExplorerStyle(athleteID int64) tileserverExplorerStyle {
	return tileserverExplorerStyle{
		Version: 8,
		Name:    fmt.Sprintf("Wanderwell Explorer Tiles %d", athleteID),
		Center:  [2]float64{0, 0},
		Zoom:    2,
		Sources: map[string]tileserverVectorSource{
			"user_explorer_tiles": {
				Type:    "vector",
				Tiles:   []string{fmt.Sprintf("http://vector-tileserver:3000/user_explorer_tiles/{z}/{x}/{y}?user_id=%d", athleteID)},
				MinZoom: 0,
				MaxZoom: 14,
			},
		},
		Layers: []tileserverFillLayer{{
			ID:          "ExplorerCoverage",
			Type:        "fill",
			Source:      "user_explorer_tiles",
			SourceLayer: "user_explorer_tiles",
			Paint: map[string]interface{}{
				"fill-color":         "rgba(203, 110, 148, 0.50)",
				"fill-opacity":       1,
				"fill-outline-color": "rgba(203, 110, 148, 0.8)",
			},
		}},
	}
}

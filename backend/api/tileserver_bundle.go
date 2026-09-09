package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"text/template"
	"time"
)

const tileserverStyleBundlePath = "/internal/tileserver/style-bundle.tar.gz"

//go:embed styles/*.json
var styleTemplateFiles embed.FS

var styleTemplates = template.Must(template.ParseFS(styleTemplateFiles, "styles/*.json"))

var bundleStyleTemplates = []bundleStyleTemplate{
	{prefix: "explorer", filename: "explorer.json"},
	{prefix: "routes", filename: "routes.json"},
}

type bundleStyleTemplate struct {
	prefix   string
	filename string
}

type styleTemplateData struct {
	AthleteID int64
}

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

	styles := make(map[string]any, len(athleteIDs)*len(bundleStyleTemplates))
	for _, athleteID := range athleteIDs {
		for _, styleTemplate := range bundleStyleTemplates {
			name := fmt.Sprintf("%s-%d", styleTemplate.prefix, athleteID)
			styles[name] = map[string]any{
				"style":    name + ".json",
				"tilejson": overlayTileJSON,
			}
		}
	}
	config := map[string]any{
		"options": map[string]any{
			"paths": map[string]string{
				"root":    "",
				"styles":  "styles",
				"fonts":   "",
				"sprites": "",
				"mbtiles": "",
			},
		},
		"styles": styles,
		"data":   map[string]any{},
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
	for _, athleteID := range athleteIDs {
		for _, styleTemplate := range bundleStyleTemplates {
			name := fmt.Sprintf("%s-%d", styleTemplate.prefix, athleteID)
			contents, err := renderStyle(styleTemplate.filename, athleteID)
			if err != nil {
				slog.Error("Failed to render TileServer GL style bundle", "style", name, "error", err)
				return
			}
			if err := writeBundleFile(tarWriter, path.Join("styles", name+".json"), contents); err != nil {
				slog.Error("Failed to write TileServer GL style bundle", "style", name, "error", err)
				return
			}
		}
	}
}

var overlayTileJSON = map[string]any{
	"type":   "overlay",
	"format": "png",
	"bounds": [4]float64{-180, -85.05112877980659, 180, 85.05112877980659},
}

func renderStyle(filename string, athleteID int64) ([]byte, error) {
	var contents bytes.Buffer
	if err := styleTemplates.ExecuteTemplate(&contents, filename, styleTemplateData{AthleteID: athleteID}); err != nil {
		return nil, err
	}
	return contents.Bytes(), nil
}

func writeBundleJSON(tw *tar.Writer, filename string, value any) error {
	contents, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeBundleFile(tw, filename, contents)
}

func writeBundleFile(tw *tar.Writer, filename string, contents []byte) error {
	if err := tw.WriteHeader(&tar.Header{
		Name:    filename,
		Mode:    0o644,
		Size:    int64(len(contents)),
		ModTime: time.Now(),
	}); err != nil {
		return err
	}
	_, err := tw.Write(contents)
	return err
}

package api

import (
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"net/http"
	"strconv"
	"wanderwell/backend/db"

	"github.com/go-chi/chi/v5"
)

const (
	explorerMinZoom   = 8
	explorerMaxZoom   = 14
	explorerTileSize  = 256
	explorerCoverageZ = 14
)

var explorerCoveredColor = color.NRGBA{R: 0, G: 0, B: 255, A: 80}
var explorerRowPatterns = buildExplorerRowPatterns()

func (s *Server) getExplorerTile(w http.ResponseWriter, r *http.Request) {
	userID, err := parseExplorerUserID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	z, x, y, err := parseTileCoordinates(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	coveredSubtiles := []db.ListExplorerCoveredSubtilesRow(nil)
	if z >= explorerMinZoom && z <= explorerMaxZoom {
		coveredSubtiles, err = s.queries.ListExplorerCoveredSubtiles(r.Context(), db.ListExplorerCoveredSubtilesParams{
			UserID: userID,
			Z:      int32(z),
			X:      int32(x),
			Y:      int32(y),
		})
		if err != nil {
			slog.Error("Failed to query explorer tile coverage", "userID", userID, "z", z, "x", x, "y", y, "error", err)
			http.Error(w, "Failed to query explorer tile", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "image/png")
	if err := png.Encode(w, generateExplorerTile(z, coveredSubtiles)); err != nil {
		slog.Error("Failed to encode explorer tile", "userID", userID, "z", z, "x", x, "y", y, "error", err)
	}
}

func parseExplorerUserID(r *http.Request) (int64, error) {
	userIDParam := r.URL.Query().Get("user_id")
	if userIDParam == "" {
		return 0, httpError("missing user_id query parameter")
	}

	userID, err := strconv.ParseInt(userIDParam, 10, 64)
	if err != nil {
		return 0, httpError("invalid user_id query parameter")
	}
	return userID, nil
}

func parseTileCoordinates(r *http.Request) (int, int, int, error) {
	z, err := parseTileCoordinateParam(chi.URLParam(r, "z"), "z")
	if err != nil {
		return 0, 0, 0, err
	}
	x, err := parseTileCoordinateParam(chi.URLParam(r, "x"), "x")
	if err != nil {
		return 0, 0, 0, err
	}
	y, err := parseTileCoordinateParam(chi.URLParam(r, "y"), "y")
	if err != nil {
		return 0, 0, 0, err
	}
	if z > 30 {
		return 0, 0, 0, httpError("z out of range")
	}

	maxTileCoordinate := 1 << z
	if x >= maxTileCoordinate || y >= maxTileCoordinate {
		return 0, 0, 0, httpError("tile coordinate out of range")
	}

	return z, x, y, nil
}

func parseTileCoordinateParam(value string, name string) (int, error) {
	coordinate, err := strconv.Atoi(value)
	if err != nil {
		return 0, httpError("invalid " + name + " path parameter")
	}
	if coordinate < 0 {
		return 0, httpError(name + " must be non-negative")
	}
	return coordinate, nil
}

func generateExplorerTile(z int, coveredSubtiles []db.ListExplorerCoveredSubtilesRow) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, explorerTileSize, explorerTileSize))
	if z < explorerMinZoom || z > explorerMaxZoom {
		return img
	}

	nTiles := 1 << (explorerCoverageZ - z)
	pixelsPerTile := explorerTileSize / nTiles
	rowPattern := explorerRowPatterns[pixelsPerTile]

	for _, subtile := range coveredSubtiles {
		x0 := int(subtile.LocalX) * pixelsPerTile
		y0 := int(subtile.LocalY) * pixelsPerTile
		fillExplorerTile(img, x0, y0, pixelsPerTile, rowPattern)
	}

	return img
}

func buildExplorerRowPatterns() map[int][]byte {
	patterns := make(map[int][]byte, explorerCoverageZ-explorerMinZoom+2)
	for pixelsPerTile := 1; pixelsPerTile <= explorerTileSize; pixelsPerTile <<= 1 {
		patterns[pixelsPerTile] = makeExplorerRowPattern(pixelsPerTile)
	}
	return patterns
}

func makeExplorerRowPattern(pixelsPerTile int) []byte {
	row := make([]byte, pixelsPerTile*4)
	for i := 0; i < len(row); i += 4 {
		row[i] = explorerCoveredColor.R
		row[i+1] = explorerCoveredColor.G
		row[i+2] = explorerCoveredColor.B
		row[i+3] = explorerCoveredColor.A
	}
	return row
}

func fillExplorerTile(img *image.NRGBA, x0 int, y0 int, pixelsPerTile int, rowPattern []byte) {
	rowWidth := pixelsPerTile * 4
	xOffset := x0 * 4
	for y := y0; y < y0+pixelsPerTile; y++ {
		offset := y*img.Stride + xOffset
		copy(img.Pix[offset:offset+rowWidth], rowPattern)
	}
}

type httpError string

func (e httpError) Error() string {
	return string(e)
}

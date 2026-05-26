package api

import (
	"image"
	"image/draw"
	"testing"

	"wanderwell/backend/db"
)

func TestGenerateExplorerTileMatchesLegacy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		z        int
		subtiles []db.ListExplorerCoveredSubtilesRow
	}{
		{
			name: "empty-z8",
			z:    8,
		},
		{
			name: "mixed-z8",
			z:    8,
			subtiles: []db.ListExplorerCoveredSubtilesRow{
				{LocalX: 0, LocalY: 0},
				{LocalX: 1, LocalY: 2},
				{LocalX: 63, LocalY: 63},
			},
		},
		{
			name: "dense-z10",
			z:    10,
			subtiles: []db.ListExplorerCoveredSubtilesRow{
				{LocalX: 0, LocalY: 0},
				{LocalX: 1, LocalY: 0},
				{LocalX: 2, LocalY: 0},
				{LocalX: 3, LocalY: 0},
				{LocalX: 4, LocalY: 1},
				{LocalX: 5, LocalY: 2},
				{LocalX: 6, LocalY: 3},
			},
		},
		{
			name: "single-pixel-z14",
			z:    14,
			subtiles: []db.ListExplorerCoveredSubtilesRow{
				{LocalX: 0, LocalY: 0},
			},
		},
		{
			name: "transparent-out-of-range",
			z:    15,
			subtiles: []db.ListExplorerCoveredSubtilesRow{
				{LocalX: 0, LocalY: 0},
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := generateExplorerTile(tc.z, tc.subtiles).(*image.NRGBA)
			want := legacyGenerateExplorerTile(tc.z, tc.subtiles)

			if got.Bounds() != want.Bounds() {
				t.Fatalf("bounds differ: got=%v want=%v", got.Bounds(), want.Bounds())
			}
			if len(got.Pix) != len(want.Pix) {
				t.Fatalf("pixel length differs: got=%d want=%d", len(got.Pix), len(want.Pix))
			}
			for i := range got.Pix {
				if got.Pix[i] != want.Pix[i] {
					t.Fatalf("pixel mismatch at %d: got=%d want=%d", i, got.Pix[i], want.Pix[i])
				}
			}
		})
	}
}

func BenchmarkGenerateExplorerTile(b *testing.B) {
	cases := []struct {
		name     string
		z        int
		subtiles []db.ListExplorerCoveredSubtilesRow
	}{
		{
			name:     "sparse-z8",
			z:        8,
			subtiles: benchmarkSubtiles(64, 3),
		},
		{
			name:     "medium-z10",
			z:        10,
			subtiles: benchmarkSubtiles(16, 48),
		},
		{
			name:     "dense-z12",
			z:        12,
			subtiles: benchmarkSubtiles(4, 12),
		},
		{
			name:     "full-z14",
			z:        14,
			subtiles: []db.ListExplorerCoveredSubtilesRow{{LocalX: 0, LocalY: 0}},
		},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.Run("legacy", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					_ = legacyGenerateExplorerTile(tc.z, tc.subtiles)
				}
			})

			b.Run("optimized", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					_ = generateExplorerTile(tc.z, tc.subtiles)
				}
			})
		})
	}
}

func legacyGenerateExplorerTile(z int, coveredSubtiles []db.ListExplorerCoveredSubtilesRow) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, explorerTileSize, explorerTileSize))
	if z < explorerMinZoom || z > explorerMaxZoom {
		return img
	}

	nTiles := 1 << (explorerCoverageZ - z)
	pixelsPerTile := explorerTileSize / nTiles
	coveredTile := &image.Uniform{C: explorerCoveredColor}

	for _, subtile := range coveredSubtiles {
		x0 := int(subtile.LocalX) * pixelsPerTile
		y0 := int(subtile.LocalY) * pixelsPerTile
		rect := image.Rect(x0, y0, x0+pixelsPerTile, y0+pixelsPerTile)
		draw.Draw(img, rect, coveredTile, image.Point{}, draw.Src)
	}

	return img
}

func benchmarkSubtiles(scale int32, count int) []db.ListExplorerCoveredSubtilesRow {
	subtiles := make([]db.ListExplorerCoveredSubtilesRow, 0, count)
	for i := 0; i < count; i++ {
		subtiles = append(subtiles, db.ListExplorerCoveredSubtilesRow{
			LocalX: int32((i * 37) % int(scale)),
			LocalY: int32((i * 53) % int(scale)),
		})
	}
	return subtiles
}

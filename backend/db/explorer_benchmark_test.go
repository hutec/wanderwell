package db_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"wanderwell/backend/db"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

const legacyExplorerQuery = `
WITH params AS (
	SELECT
		$1::bigint AS user_id,
		$2::integer AS z,
		$3::integer AS x,
		$4::integer AS y,
		(1 << (14 - $2::integer))::integer AS scale
),
parent_tile AS (
	SELECT ST_Transform(ST_TileEnvelope(p.z, p.x, p.y), 4326) AS geom
	FROM params p
),
candidate_routes AS (
	SELECT route.geom
	FROM route, params p, parent_tile pt
	WHERE route.user_id = p.user_id
	  AND route.geom IS NOT NULL
	  AND route.geom && pt.geom
	  AND ST_Intersects(route.geom, pt.geom)
),
subtiles AS (
	SELECT
		child_x - (p.x * p.scale) AS local_x,
		child_y - (p.y * p.scale) AS local_y,
		ST_Transform(ST_TileEnvelope(14, child_x, child_y), 4326) AS geom
	FROM params p
	CROSS JOIN generate_series(p.x * p.scale, ((p.x + 1) * p.scale) - 1) AS child_x
	CROSS JOIN generate_series(p.y * p.scale, ((p.y + 1) * p.scale) - 1) AS child_y
)
SELECT subtiles.local_x, subtiles.local_y
FROM subtiles
WHERE EXISTS (
	SELECT 1
	FROM candidate_routes
	WHERE candidate_routes.geom && subtiles.geom
	  AND ST_Intersects(candidate_routes.geom, subtiles.geom)
)
ORDER BY subtiles.local_y, subtiles.local_x
`

type benchmarkCase struct {
	userID int64
	z      int32
	x      int32
	y      int32
}

func TestExplorerQueryRowsInBounds(t *testing.T) {
	pool := openExplorerBenchmarkDB(t)
	defer pool.Close()

	cases := loadExplorerBenchmarkCases(t, pool)
	if len(cases) == 0 {
		t.Skip("no explorer benchmark cases found")
	}

	queries := db.New(pool)
	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("user-%d-z%d-%d-%d", tc.userID, tc.z, tc.x, tc.y), func(t *testing.T) {
			newRows, err := queries.ListExplorerCoveredSubtiles(context.Background(), db.ListExplorerCoveredSubtilesParams{
				UserID: tc.userID,
				Z:      tc.z,
				X:      tc.x,
				Y:      tc.y,
			})
			if err != nil {
				t.Fatalf("optimized query failed: %v", err)
			}

			scale := int32(1 << (14 - tc.z))
			seen := make(map[[2]int32]struct{}, len(newRows))
			for i, row := range newRows {
				if row.LocalX < 0 || row.LocalX >= scale || row.LocalY < 0 || row.LocalY >= scale {
					t.Fatalf("row out of bounds: %+v scale=%d", row, scale)
				}

				key := [2]int32{row.LocalX, row.LocalY}
				if _, exists := seen[key]; exists {
					t.Fatalf("duplicate row returned: %+v", row)
				}
				seen[key] = struct{}{}

				if i == 0 {
					continue
				}
				prev := newRows[i-1]
				if row.LocalY < prev.LocalY || (row.LocalY == prev.LocalY && row.LocalX <= prev.LocalX) {
					t.Fatalf("rows are not strictly ordered: prev=%+v current=%+v", prev, row)
				}
			}
		})
	}
}

func TestExplorerQueryHarness(t *testing.T) {
	if os.Getenv("EXPLORER_BENCHMARK") == "" {
		t.Skip("set EXPLORER_BENCHMARK=1 to run the explorer benchmark harness")
	}

	pool := openExplorerBenchmarkDB(t)
	defer pool.Close()

	cases := loadExplorerBenchmarkCases(t, pool)
	queries := db.New(pool)

	var legacyDurations []time.Duration
	var optimizedDurations []time.Duration

	for _, tc := range cases {
		legacyDuration := timeQuery(t, func(ctx context.Context) error {
			_ = runLegacyExplorerQuery(t, pool, tc)
			return nil
		})
		optimizedDuration := timeQuery(t, func(ctx context.Context) error {
			_, err := queries.ListExplorerCoveredSubtiles(ctx, db.ListExplorerCoveredSubtilesParams{
				UserID: tc.userID,
				Z:      tc.z,
				X:      tc.x,
				Y:      tc.y,
			})
			return err
		})

		legacyDurations = append(legacyDurations, legacyDuration)
		optimizedDurations = append(optimizedDurations, optimizedDuration)

		t.Logf(
			"user=%d z=%d x=%d y=%d legacy=%s optimized=%s speedup=%.2fx",
			tc.userID,
			tc.z,
			tc.x,
			tc.y,
			legacyDuration,
			optimizedDuration,
			float64(legacyDuration)/float64(optimizedDuration),
		)
	}

	legacyMedian := medianDuration(legacyDurations)
	optimizedMedian := medianDuration(optimizedDurations)
	t.Logf(
		"median legacy=%s optimized=%s speedup=%.2fx",
		legacyMedian,
		optimizedMedian,
		float64(legacyMedian)/float64(optimizedMedian),
	)
}

func BenchmarkExplorerQueryVariants(b *testing.B) {
	pool := openExplorerBenchmarkDB(b)
	defer pool.Close()

	cases := loadExplorerBenchmarkCases(b, pool)
	queries := db.New(pool)

	b.Run("legacy", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = runLegacyExplorerQuery(b, pool, cases[i%len(cases)])
		}
	})

	b.Run("optimized", func(b *testing.B) {
		ctx := context.Background()
		for i := 0; i < b.N; i++ {
			tc := cases[i%len(cases)]
			if _, err := queries.ListExplorerCoveredSubtiles(ctx, db.ListExplorerCoveredSubtilesParams{
				UserID: tc.userID,
				Z:      tc.z,
				X:      tc.x,
				Y:      tc.y,
			}); err != nil {
				b.Fatalf("optimized query failed: %v", err)
			}
		}
	})
}

func openExplorerBenchmarkDB(tb testing.TB) *pgxpool.Pool {
	tb.Helper()

	databaseURL := os.Getenv("EXPLORER_BENCHMARK_DATABASE_URL")
	if databaseURL == "" {
		_ = godotenv.Load(
			filepath.Join("..", "..", ".env"),
			filepath.Join("..", ".env"),
			".env",
		)
		if password := os.Getenv("POSTGRES_PASSWORD"); password != "" {
			databaseURL = fmt.Sprintf("postgres://postgres:%s@localhost:5432/wanderwell?sslmode=disable", password)
		}
	}
	if databaseURL == "" {
		tb.Skip("set EXPLORER_BENCHMARK_DATABASE_URL or POSTGRES_PASSWORD in .env to run explorer DB benchmarks")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		tb.Fatalf("failed to create pgx pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		tb.Skipf("benchmark database unavailable: %v", err)
	}
	return pool
}

func loadExplorerBenchmarkCases(tb testing.TB, pool *pgxpool.Pool) []benchmarkCase {
	tb.Helper()

	const benchmarkCasesQuery = `
WITH top_users AS (
	SELECT user_id
	FROM route
	WHERE geom IS NOT NULL
	GROUP BY user_id
	ORDER BY COUNT(*) DESC
	LIMIT 2
),
centroids AS (
	SELECT route.user_id, ST_Centroid(ST_Collect(route.geom)) AS geom
	FROM route
	JOIN top_users ON top_users.user_id = route.user_id
	WHERE route.geom IS NOT NULL
	GROUP BY route.user_id
)
SELECT
	centroids.user_id,
	zooms.z,
	FLOOR((ST_X(centroids.geom) + 180.0) / 360.0 * (1 << zooms.z))::integer AS x,
	FLOOR((1.0 - LN(TAN(RADIANS(ST_Y(centroids.geom))) + 1.0 / COS(RADIANS(ST_Y(centroids.geom)))) / PI()) / 2.0 * (1 << zooms.z))::integer AS y
FROM centroids
CROSS JOIN (VALUES (8), (10), (12), (14)) AS zooms(z)
ORDER BY centroids.user_id, zooms.z
`

	rows, err := pool.Query(context.Background(), benchmarkCasesQuery)
	if err != nil {
		tb.Fatalf("failed to load benchmark cases: %v", err)
	}
	defer rows.Close()

	var cases []benchmarkCase
	for rows.Next() {
		var tc benchmarkCase
		if err := rows.Scan(&tc.userID, &tc.z, &tc.x, &tc.y); err != nil {
			tb.Fatalf("failed to scan benchmark case: %v", err)
		}
		cases = append(cases, tc)
	}
	if err := rows.Err(); err != nil {
		tb.Fatalf("failed to iterate benchmark cases: %v", err)
	}
	if len(cases) == 0 {
		tb.Fatalf("no benchmark cases found")
	}
	return cases
}

func runLegacyExplorerQuery(tb testing.TB, pool *pgxpool.Pool, tc benchmarkCase) []db.ListExplorerCoveredSubtilesRow {
	tb.Helper()

	rows, err := pool.Query(context.Background(), legacyExplorerQuery, tc.userID, tc.z, tc.x, tc.y)
	if err != nil {
		tb.Fatalf("legacy query failed: %v", err)
	}
	defer rows.Close()

	var out []db.ListExplorerCoveredSubtilesRow
	for rows.Next() {
		var row db.ListExplorerCoveredSubtilesRow
		if err := rows.Scan(&row.LocalX, &row.LocalY); err != nil {
			tb.Fatalf("failed to scan legacy row: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		tb.Fatalf("failed to iterate legacy rows: %v", err)
	}
	return out
}

func timeQuery(tb testing.TB, fn func(ctx context.Context) error) time.Duration {
	tb.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	start := time.Now()
	if err := fn(ctx); err != nil {
		tb.Fatalf("timed query failed: %v", err)
	}
	return time.Since(start)
}

func medianDuration(values []time.Duration) time.Duration {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	return sorted[len(sorted)/2]
}

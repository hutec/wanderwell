package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"time"

	"wanderwell/backend/db"
	"wanderwell/backend/models"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/twpayne/go-polyline"
	_ "modernc.org/sqlite" // SQLite driver
)

// Route struct matches the schema of the route table in SQLite
type Route struct {
	ID           int64
	UserID       int64
	StartDate    time.Time
	Name         string
	ElapsedTime  int32
	MovingTime   int32
	Distance     float32
	AverageSpeed float32
	Route        string // Polyline string
	Elevation    float32
	Bounds       string
}

type User struct {
	ID           int64  `json:"id"`
	Firstname    string `json:"name"`
	Lastname     string `json:"-"`
	ExpiresAt    int64  `json:"-"`
	RefreshToken string `json:"-"`
	AccessToken  string `json:"-"`
}

func main() {
	// Define command-line flags
	sqlitePath := flag.String("sqlite", "elmo.db", "Path to SQLite database")
	postgisConn := flag.String("postgis", "", "PostgreSQL connection string (e.g., postgres://user:pass@localhost/dbname?sslmode=disable)")
	flag.Parse()

	// Validate required flags
	if *postgisConn == "" {
		log.Fatal("Error: -postgis flag is required\n\nUsage example:\n  go run cmd/export/postgis.go -sqlite=elmo.db -postgis=\"postgres://user:pass@localhost/dbname?sslmode=disable\"")
	}

	// Open SQLite database
	log.Printf("Opening SQLite database: %s", *sqlitePath)
	sqliteDB, err := sql.Open("sqlite", *sqlitePath)
	if err != nil {
		log.Fatalf("Failed to open SQLite database: %v", err)
	}
	defer sqliteDB.Close()

	// Test SQLite connection
	if err := sqliteDB.Ping(); err != nil {
		log.Fatalf("Failed to ping SQLite database: %v", err)
	}

	// Copy data to PostGIS
	log.Printf("Copying routes to PostGIS...")
	if err := copyToPostGIS(sqliteDB, *postgisConn); err != nil {
		log.Fatalf("Failed to copy to PostGIS: %v", err)
	}

	log.Println("Export completed successfully!")
}

func copyToPostGIS(sqliteDB *sql.DB, postgisConnStr string) error {
	ctx := context.Background()

	// Connect to PostGIS using native pgx
	pgConn, err := pgx.Connect(ctx, postgisConnStr)
	if err != nil {
		return fmt.Errorf("failed to connect to PostGIS: %w", err)
	}
	defer pgConn.Close(ctx)

	queries := db.New(pgConn)

	// Query all users from SQLite
	log.Println("Querying users from SQLite...")
	userRows, err := sqliteDB.Query(`
        SELECT id, firstname, lastname, expires_at, refresh_token, access_token
        FROM user
    `)
	if err != nil {
		return fmt.Errorf("failed to query users: %w", err)
	}
	defer userRows.Close()

	userCount := 0
	userSkipped := 0
	log.Println("Copying users...")
	for userRows.Next() {
		var u User
		err := userRows.Scan(&u.ID, &u.Firstname, &u.Lastname, &u.ExpiresAt, &u.RefreshToken, &u.AccessToken)
		if err != nil {
			log.Printf("Error scanning user: %v", err)
			userSkipped++
			continue
		}

		err = queries.UpsertAthlete(ctx, db.UpsertAthleteParams{
			ID:           u.ID,
			Firstname:    pgtype.Text{String: u.Firstname, Valid: true},
			Lastname:     pgtype.Text{String: u.Lastname, Valid: true},
			AccessToken:  pgtype.Text{String: u.AccessToken, Valid: true},
			RefreshToken: pgtype.Text{String: u.RefreshToken, Valid: true},
			ExpiresAt:    pgtype.Int8{Int64: u.ExpiresAt, Valid: true},
		})
		if err != nil {
			log.Printf("Error inserting user %d: %v", u.ID, err)
			userSkipped++
			continue
		}
		userCount++
	}

	log.Printf("Successfully copied %d users to PostGIS (skipped %d)", userCount, userSkipped)

	// Query all routes from SQLite
	log.Println("Querying routes from SQLite...")
	rows, err := sqliteDB.Query(`
        SELECT id, user_id, start_date, name, elapsed_time, moving_time,
               distance, average_speed, route, elevation, bounds
        FROM route
    `)
	if err != nil {
		return fmt.Errorf("failed to query routes: %w", err)
	}
	defer rows.Close()

	count := 0
	skipped := 0
	log.Println("Copying routes...")
	for rows.Next() {
		var r Route
		err := rows.Scan(&r.ID, &r.UserID, &r.StartDate, &r.Name, &r.ElapsedTime,
			&r.MovingTime, &r.Distance, &r.AverageSpeed, &r.Route,
			&r.Elevation, &r.Bounds)
		if err != nil {
			log.Printf("Error scanning route: %v", err)
			skipped++
			continue
		}

		// Decode polyline to coordinates
		coords, _, err := polyline.DecodeCoords([]byte(r.Route))
		if err != nil {
			log.Printf("Error decoding polyline for route %d: %v", r.ID, err)
			skipped++
			continue
		}

		// Convert to WKT LineString format
		wkt := models.CoordsToWKT(coords)

		err = queries.UpsertRoute(ctx, db.UpsertRouteParams{
			ID:             r.ID,
			UserID:         r.UserID,
			StartDate:      pgtype.Timestamptz{Time: r.StartDate, Valid: true},
			Name:           r.Name,
			ElapsedTime:    r.ElapsedTime,
			MovingTime:     r.MovingTime,
			Distance:       float64(r.Distance),
			AverageSpeed:   float64(r.AverageSpeed),
			Elevation:      float64(r.Elevation),
			Bounds:         r.Bounds,
			StGeomfromtext: wkt,
		})
		if err != nil {
			log.Printf("Error inserting route %d: %v", r.ID, err)
			skipped++
			continue
		}
		count++

		// Print progress every 100 routes
		if count%100 == 0 {
			log.Printf("Copied %d routes...", count)
		}
	}

	log.Printf("Successfully copied %d routes to PostGIS (skipped %d)", count, skipped)
	return nil
}

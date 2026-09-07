package main

import (
	"context"
	"fmt"
	"log"

	"github.com/filetransfer/backend/internal/config"
	"github.com/filetransfer/backend/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type seedUser struct {
	Username string
	Password string
}

var seedUsers = []seedUser{
	{"alice", "password123"},
	{"bob", "password123"},
	{"charlie", "password123"},
	{"diana", "password123"},
	{"eve", "password123"},
	{"frank", "password123"},
	{"grace", "password123"},
	{"heidi", "password123"},
	{"ivan", "password123"},
	{"judy", "password123"},
}

// roomPair indexes into seedUsers
type roomPair struct{ a, b int }

var seedRooms = []roomPair{
	{0, 1}, // alice <-> bob
	{0, 2}, // alice <-> charlie
	{1, 3}, // bob <-> diana
	{2, 4}, // charlie <-> eve
	{5, 6}, // frank <-> grace
}

func main() {
	cfg := config.Load()

	pool := db.Connect(cfg.DatabaseURL)
	defer pool.Close()

	// Auto-create schema if not exists
	db.RunMigrations(pool)

	ctx := context.Background()
	fmt.Println("\nSeeding users...")

	userIDs := make([]string, len(seedUsers))
	for i, u := range seedUsers {
		id, err := upsertUser(ctx, pool, u.Username, u.Password)
		if err != nil {
			log.Fatalf("Failed to seed user %s: %v", u.Username, err)
		}
		userIDs[i] = id
	}

	fmt.Println("\nSeeding rooms...")
	for _, rp := range seedRooms {
		err := upsertRoom(ctx, pool, userIDs[rp.a], userIDs[rp.b],
			seedUsers[rp.a].Username, seedUsers[rp.b].Username)
		if err != nil {
			log.Fatalf("Failed to seed room: %v", err)
		}
	}

	fmt.Println("\nSeed complete!")
	fmt.Println("All users have password: password123")
	fmt.Printf("Usernames: ")
	for i, u := range seedUsers {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Print(u.Username)
	}
	fmt.Println()
}

func upsertUser(ctx context.Context, pool *pgxpool.Pool, username, password string) (string, error) {
	// Check if user already exists
	var existingID string
	err := pool.QueryRow(ctx,
		"SELECT id FROM users WHERE username = $1", username,
	).Scan(&existingID)
	if err == nil {
		fmt.Printf("  User %q already exists (id: %s), skipping.\n", username, existingID)
		return existingID, nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}

	var id string
	err = pool.QueryRow(ctx,
		"INSERT INTO users (username, password_hash) VALUES ($1, $2) RETURNING id",
		username, string(hash),
	).Scan(&id)
	if err != nil {
		return "", err
	}

	fmt.Printf("  Created user %q (id: %s)\n", username, id)
	return id, nil
}

func upsertRoom(ctx context.Context, pool *pgxpool.Pool, userAID, userBID, userAName, userBName string) error {
	// Ensure consistent ordering (smaller UUID as user1)
	user1, user2 := userAID, userBID
	if user1 > user2 {
		user1, user2 = user2, user1
	}

	// Check if room exists
	var existingID string
	err := pool.QueryRow(ctx,
		"SELECT id FROM rooms WHERE user1_id = $1 AND user2_id = $2", user1, user2,
	).Scan(&existingID)
	if err == nil {
		fmt.Printf("  Room %s <-> %s already exists, skipping.\n", userAName, userBName)
		return nil
	}

	_, err = pool.Exec(ctx,
		"INSERT INTO rooms (user1_id, user2_id) VALUES ($1, $2)", user1, user2,
	)
	if err != nil {
		return err
	}

	fmt.Printf("  Created room: %s <-> %s\n", userAName, userBName)
	return nil
}

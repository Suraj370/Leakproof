package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"leakproof/internal/api"
	"leakproof/internal/catalog"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	srv := &api.Server{Cat: catalog.Seed()}

	// DATABASE_URL switches the reference data from the in-memory demo seed to PostgreSQL
	// (optionally cached in Redis). Only reference data is ever read: no user data touches either store.
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		ctx := context.Background()
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			log.Fatalf("database: %v", err)
		}
		defer pool.Close()

		var rc *redis.Client
		if u := os.Getenv("REDIS_URL"); u != "" {
			opt, err := redis.ParseURL(u)
			if err != nil {
				log.Fatalf("redis url: %v", err)
			}
			rc = redis.NewClient(opt)
		}
		cached := catalog.Cached{Redis: rc, Key: "leakproof:catalog:v1", TTL: 15 * time.Minute,
			Load: func(ctx context.Context) (*catalog.Catalog, error) { return catalog.Load(ctx, pool) }}

		c, err := cached.Get(ctx)
		if err != nil {
			log.Fatalf("load catalog: %v", err)
		}
		srv.SetCatalog(c)
		log.Printf("catalog loaded from database: %d funds, %d cards", len(c.Funds), len(c.Cards))

		go func() {
			for range time.Tick(10 * time.Minute) {
				c, err := cached.Get(context.Background())
				if err != nil {
					log.Printf("catalog refresh failed (keeping previous): %v", err)
					continue
				}
				srv.SetCatalog(c)
			}
		}()
	} else {
		log.Printf("DATABASE_URL not set: serving the illustrative demo catalog")
	}

	app := api.NewApp(srv)
	// Only the listen address is logged. Request bodies and headers are never logged (guest mode).
	log.Printf("leakproof api listening on :%s", port)
	log.Fatal(app.Listen(":" + port))
}

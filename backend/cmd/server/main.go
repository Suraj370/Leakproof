package main

import (
	"log"
	"os"

	"wealthleak/internal/api"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	app := api.NewApp(&api.Server{Cat: api.SeedCatalog()})
	// Only the listen address is logged. Request bodies and headers are never logged (guest mode).
	log.Printf("wealthleak api listening on :%s", port)
	log.Fatal(app.Listen(":" + port))
}

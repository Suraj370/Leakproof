package api

import "leakproof/internal/catalog"

type Catalog = catalog.Catalog

// SeedCatalog returns the illustrative in-memory demo catalog (used when DATABASE_URL is not set).
func SeedCatalog() *Catalog { return catalog.Seed() }

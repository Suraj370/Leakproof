package catalog

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"leakproof/internal/cards"
	"leakproof/internal/mf"
)

type fundJSON struct {
	mf.Fund
	Holdings     map[string]int64  `json:"holdings"`
	HoldingNames map[string]string `json:"holdingNames"`
	GrandfatherN string            `json:"grandfatherNav"` // decimal string; "" = unknown
}

type snapshot struct {
	Funds []fundJSON   `json:"funds"`
	Cards []cards.Card `json:"cards"`
}

// Marshal serialises the catalog (including holdings, which mf.Fund hides from API JSON).
func Marshal(c *Catalog) ([]byte, error) {
	s := snapshot{Cards: c.Cards}
	for _, f := range c.Funds {
		fj := fundJSON{Fund: f, Holdings: f.Holdings, HoldingNames: f.HoldingNames}
		if !f.GrandfatherNAV.IsZero() {
			fj.GrandfatherN = f.GrandfatherNAV.String()
		}
		s.Funds = append(s.Funds, fj)
	}
	sort.Slice(s.Funds, func(i, j int) bool { return s.Funds[i].ISIN < s.Funds[j].ISIN })
	return json.Marshal(s)
}

func Unmarshal(b []byte) (*Catalog, error) {
	var s snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	c := &Catalog{Funds: make(map[string]mf.Fund, len(s.Funds)), Cards: s.Cards}
	c.normalize()
	for _, f := range s.Funds {
		f.Fund.Holdings, f.Fund.HoldingNames = f.Holdings, f.HoldingNames
		if f.GrandfatherN != "" {
			n, err := decimal.NewFromString(f.GrandfatherN)
			if err != nil {
				return nil, err
			}
			f.Fund.GrandfatherNAV = n
		}
		c.Funds[f.ISIN] = f.Fund
	}
	return c, nil
}

// Cached wraps a loader with a Redis read-through cache. Redis only ever holds reference data
// (never user data), and any Redis failure falls back to the loader so it can't take the API down.
type Cached struct {
	Redis *redis.Client
	Key   string
	TTL   time.Duration
	Load  func(context.Context) (*Catalog, error)
}

func (c Cached) Get(ctx context.Context) (*Catalog, error) {
	if c.Redis != nil {
		if b, err := c.Redis.Get(ctx, c.Key).Bytes(); err == nil {
			if cat, err := Unmarshal(b); err == nil {
				return cat, nil
			}
		}
	}
	cat, err := c.Load(ctx)
	if err != nil {
		return nil, err
	}
	if c.Redis != nil {
		if b, err := Marshal(cat); err == nil {
			_ = c.Redis.Set(ctx, c.Key, b, c.TTL).Err() // best effort
		}
	}
	return cat, nil
}

// Invalidate drops the cached catalog; the ingestion jobs call this after a successful swap.
func (c Cached) Invalidate(ctx context.Context) error {
	if c.Redis == nil {
		return nil
	}
	return c.Redis.Del(ctx, c.Key).Err()
}

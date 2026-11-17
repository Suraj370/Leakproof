package mf

import (
	"context"
	"sort"
	"time"

	"golang.org/x/sync/errgroup"
)

type SharedHolding struct {
	StockISIN  string `json:"stockIsin"`
	Name       string `json:"name"`
	WeightABps int64  `json:"weightABps"`
	WeightBBps int64  `json:"weightBBps"`
	OverlapBps int64  `json:"overlapBps"` // min(wA, wB)
}

type PairResult struct {
	A          int             `json:"a"`
	B          int             `json:"b"`
	OverlapBps int64           `json:"overlapBps"`
	Shared     []SharedHolding `json:"shared"` // top 15 by overlap
	Action     PairAction      `json:"action"`
}

// Overlap(A,B) = sum over shared stocks of min(W_A, W_B), in basis points.
func Overlap(fa, fb Fund) (total int64, shared []SharedHolding) {
	small, big, flip := fa.Holdings, fb.Holdings, false
	if len(small) > len(big) {
		small, big, flip = big, small, true
	}
	for isin, ws := range small {
		wb, ok := big[isin]
		if !ok {
			continue
		}
		m := min(ws, wb)
		total += m
		wa, wB := ws, wb
		if flip {
			wa, wB = wb, ws
		}
		name := fa.HoldingNames[isin]
		if name == "" {
			name = fb.HoldingNames[isin]
		}
		shared = append(shared, SharedHolding{isin, name, wa, wB, m})
	}
	sort.Slice(shared, func(i, j int) bool {
		if shared[i].OverlapBps != shared[j].OverlapBps {
			return shared[i].OverlapBps > shared[j].OverlapBps
		}
		return shared[i].StockISIN < shared[j].StockISIN
	})
	if len(shared) > 15 {
		shared = shared[:15]
	}
	return
}

// ComputeOverlaps fans the N*(N-1)/2 pairs over a bounded worker pool. Each goroutine writes its own
// slice slot, so no mutex is needed. Results are in deterministic (i,j) order.
func ComputeOverlaps(ctx context.Context, funds []Fund, workers int) ([]PairResult, error) {
	type ij struct{ i, j int }
	var idx []ij
	for i := range funds {
		for j := i + 1; j < len(funds); j++ {
			idx = append(idx, ij{i, j})
		}
	}
	out := make([]PairResult, len(idx))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(max(1, workers))
	for k, p := range idx {
		g.Go(func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			tot, sh := Overlap(funds[p.i], funds[p.j])
			out[k] = PairResult{A: p.i, B: p.j, OverlapBps: tot, Shared: sh, Action: PairAction{Verdict: Keep}}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

// Analyze flags >30% pairs, picks the higher-TER fund as the exit candidate and attaches a tax-aware
// verdict. It also returns the annual fee leak (each exit fund counted once, at its largest saving).
func Analyze(ctx context.Context, funds []Fund, pos map[string]Position, cfg TaxConfig,
	now time.Time, realisedLTCG int64, workers int) ([]PairResult, int64, error) {

	pairs, err := ComputeOverlaps(ctx, funds, workers)
	if err != nil {
		return nil, 0, err
	}
	leak := map[int]int64{}
	for k := range pairs {
		p := &pairs[k]
		if p.OverlapBps <= FlagThresholdBps {
			continue
		}
		keepI, exitI := p.A, p.B
		if funds[p.A].TERBps > funds[p.B].TERBps {
			keepI, exitI = p.B, p.A
		}
		p.Action = cfg.Judge(funds[exitI], funds[keepI], pos[funds[exitI].ISIN], now, realisedLTCG)
		p.Action.ExitFundIndex, p.Action.KeepFundIndex = exitI, keepI
		if p.Action.Verdict != Keep {
			leak[exitI] = max(leak[exitI], p.Action.AnnualSavingPaise)
		}
	}
	var total int64
	for _, v := range leak {
		total += v
	}
	return pairs, total, nil
}

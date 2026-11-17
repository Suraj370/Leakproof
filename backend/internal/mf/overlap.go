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

// Analyze flags >30% pairs, picks the higher-TER fund of each as the exit candidate and attaches a
// tax-aware verdict. The Rs1.25L LTCG exemption is ONE pool per financial year, so flagged pairs are judged
// in descending order of annual saving and each CONSOLIDATE_NOW exit consumes part of the pool. An exit
// fund that appears in several pairs is a single redemption: it consumes the pool once and never competes
// with itself. Returns the pairs and the annual fee leak (each exit fund counted once, at its largest saving).
func Analyze(ctx context.Context, funds []Fund, pos map[string]Position, cfg TaxConfig,
	now time.Time, realisedLTCG int64, workers int) ([]PairResult, int64, error) {

	pairs, err := ComputeOverlaps(ctx, funds, workers)
	if err != nil {
		return nil, 0, err
	}

	type flagged struct {
		k, keepI, exitI int
		saving          int64
	}
	var order []flagged
	for k := range pairs {
		p := &pairs[k]
		if p.OverlapBps <= FlagThresholdBps {
			continue
		}
		keepI, exitI := p.A, p.B
		if funds[p.A].TERBps > funds[p.B].TERBps {
			keepI, exitI = p.B, p.A
		}
		saving := bps(pos[funds[exitI].ISIN].ValuePaise(), funds[exitI].TERBps-funds[keepI].TERBps)
		order = append(order, flagged{k, keepI, exitI, saving})
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].saving > order[j].saving })

	remaining := max(0, cfg.LTCGExemptionPaise-realisedLTCG)
	usedBy := map[int]int64{} // exit fund -> exemption its CONSOLIDATE_NOW consumed
	counted := map[int]bool{}
	leak := map[int]int64{}
	for _, f := range order {
		p := &pairs[f.k]
		avail := remaining + usedBy[f.exitI] // an exit already counted does not compete with itself
		act := cfg.Judge(funds[f.exitI], funds[f.keepI], pos[funds[f.exitI].ISIN], now, cfg.LTCGExemptionPaise-avail)
		act.ExitFundIndex, act.KeepFundIndex = f.exitI, f.keepI
		p.Action = act
		if act.Verdict == ConsolidateNow && !counted[f.exitI] {
			remaining -= act.ExitNow.ExemptionUsedPaise
			usedBy[f.exitI] = act.ExitNow.ExemptionUsedPaise
			counted[f.exitI] = true
		}
		if act.Verdict != Keep {
			leak[f.exitI] = max(leak[f.exitI], act.AnnualSavingPaise)
		}
	}
	var total int64
	for _, v := range leak {
		total += v
	}
	return pairs, total, nil
}

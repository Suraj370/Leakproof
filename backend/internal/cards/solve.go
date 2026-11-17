package cards

import (
	"math"
	"sort"
)

// Exact allocation of a monthly spend profile across a set of cards.
//
// Caps are per statement cycle and make each card's reward concave and piecewise-linear in what it is
// given, while milestone bonuses and fee waivers are step functions of the card's yearly eligible spend.
// So the problem is a linear program once every card's "step level" is fixed. We enumerate the levels
// (a handful per card), solve one LP per combination, and keep the allocation with the best REAL value
// (computed by NetYield, with its per-category point flooring). The result is optimal up to that
// flooring (under Rs1 per category per month) and never worse than the greedy allocator, which is kept
// as a safety net and as the fallback when the state space or the LP is too large.

const maxLevelStates = 729

type threshold struct{ spendPaise, bonusPaise int64 }

// thresholdsOf lists a card's yearly-eligible-spend steps: milestones plus the fee waiver (worth the fee).
func thresholdsOf(c Card) []threshold {
	var ts []threshold
	for _, m := range c.Milestones {
		ts = append(ts, threshold{m.AnnualSpendPaise, m.BonusValuePaise})
	}
	if c.AnnualFeePaise > 0 && c.FeeWaiverSpendPaise > 0 {
		ts = append(ts, threshold{c.FeeWaiverSpendPaise, c.AnnualFeePaise})
	}
	sort.SliceStable(ts, func(i, j int) bool { return ts[i].spendPaise < ts[j].spendPaise })
	return ts
}

type model struct {
	cards  []Card
	cats   []Category // categories with spend, excluding EXCLUDED, sorted
	spend  []float64  // rupees per month, per cat
	paise  []int64
	xi     [][]int // [card][cat] -> variable index, or -1 when the card earns nothing there (excluded)
	nvars  int
	c      []float64
	rows   []lpRow
	capped []bool // per x variable: reward comes through the group's points variable
}

func buildModel(cards []Card, spend Spend) *model {
	m := &model{cards: cards}
	for cat, v := range spend {
		if v > 0 && cat != Excluded {
			m.cats = append(m.cats, cat)
		}
	}
	sort.Slice(m.cats, func(i, j int) bool { return m.cats[i] < m.cats[j] })
	for _, cat := range m.cats {
		m.paise = append(m.paise, spend[cat])
		m.spend = append(m.spend, float64(spend[cat])/100)
	}

	// x variables
	m.xi = make([][]int, len(cards))
	for k, cd := range cards {
		m.xi[k] = make([]int, len(m.cats))
		for i, cat := range m.cats {
			if cd.rule(cat).Excluded {
				m.xi[k][i] = -1
				continue
			}
			m.xi[k][i] = m.nvars
			m.nvars++
		}
	}
	nx := m.nvars

	// points variables, one per (card, capped group in use)
	type pv struct {
		card  int
		group string
		idx   int
	}
	var ps []pv
	for k, cd := range cards {
		seen := map[string]bool{}
		var groups []string
		for i, cat := range m.cats {
			if m.xi[k][i] < 0 {
				continue
			}
			g := cd.rule(cat).CapGroup
			if _, capped := cd.GroupCapPoints[g]; g != "" && capped && !seen[g] {
				seen[g] = true
				groups = append(groups, g)
			}
		}
		sort.Strings(groups)
		for _, g := range groups {
			ps = append(ps, pv{k, g, m.nvars})
			m.nvars++
		}
	}

	m.c = make([]float64, m.nvars)
	m.capped = make([]bool, nx)
	for k, cd := range cards {
		for i, cat := range m.cats {
			j := m.xi[k][i]
			if j < 0 {
				continue
			}
			r := cd.rule(cat)
			if _, capped := cd.GroupCapPoints[r.CapGroup]; r.CapGroup != "" && capped {
				m.capped[j] = true // reward flows through the points variable
				continue
			}
			m.c[j] = float64(12*cd.RedeemPaisePerPoint*r.EarnBps) / 10000.0
		}
	}
	zero := func() []float64 { return make([]float64, m.nvars) }

	// each category's spend is shared between the cards
	for i := range m.cats {
		row := zero()
		for k := range cards {
			if j := m.xi[k][i]; j >= 0 {
				row[j] = 1
			}
		}
		m.rows = append(m.rows, lpRow{row, rowLE, m.spend[i]})
	}
	// points earned in a capped group: p <= cap and p <= sum(earn * x)
	for _, p := range ps {
		cd := cards[p.card]
		m.c[p.idx] = float64(12 * cd.RedeemPaisePerPoint)
		capRow := zero()
		capRow[p.idx] = 1
		m.rows = append(m.rows, lpRow{capRow, rowLE, float64(cd.GroupCapPoints[p.group])})

		link := zero()
		link[p.idx] = 1
		for i, cat := range m.cats {
			if j := m.xi[p.card][i]; j >= 0 && cd.rule(cat).CapGroup == p.group {
				link[j] = -(float64(cd.rule(cat).EarnBps) / 10000.0)
			}
		}
		m.rows = append(m.rows, lpRow{link, rowLE, 0})
	}
	return m
}

// withLevels adds, for every card at level l>0, the constraint "yearly eligible spend >= threshold l".
func (m *model) withLevels(ts [][]threshold, level []int) []lpRow {
	rows := append([]lpRow(nil), m.rows...)
	for k, l := range level {
		if l == 0 {
			continue
		}
		row := make([]float64, m.nvars)
		for i := range m.cats {
			if j := m.xi[k][i]; j >= 0 {
				row[j] = 1
			}
		}
		rows = append(rows, lpRow{row, rowGE, float64(ts[k][l-1].spendPaise) / 1200.0})
	}
	return rows
}

// toAlloc rounds an LP solution to whole paise, never over-assigning a category, and nudges cards that
// must reach a step so rounding cannot leave them a paise short.
func (m *model) toAlloc(x []float64, ts [][]threshold, level []int) map[string]Spend {
	nk, ni := len(m.cards), len(m.cats)
	a := make([][]int64, nk)
	for k := range a {
		a[k] = make([]int64, ni)
		for i := 0; i < ni; i++ {
			if j := m.xi[k][i]; j >= 0 {
				a[k][i] = max(0, int64(math.Floor(float64(x[j]*100)+0.5)))
			}
		}
	}
	used := func(i int) (s int64) {
		for k := 0; k < nk; k++ {
			s += a[k][i]
		}
		return
	}
	for i := 0; i < ni; i++ {
		for used(i) > m.paise[i] { // rounding overshoot: trim the largest holder
			big := 0
			for k := 1; k < nk; k++ {
				if a[k][i] > a[big][i] {
					big = k
				}
			}
			a[big][i]--
		}
	}
	for k, l := range level {
		if l == 0 {
			continue
		}
		need := (ts[k][l-1].spendPaise + 11) / 12 // monthly paise so that 12x >= threshold
		var have int64
		for i := 0; i < ni; i++ {
			have += a[k][i]
		}
		for d := need - have; d > 0; {
			moved := false
			for i := 0; i < ni && d > 0; i++ {
				if m.xi[k][i] < 0 {
					continue
				}
				if spare := m.paise[i] - used(i); spare > 0 {
					t := min(d, spare)
					a[k][i] += t
					d -= t
					moved = true
					continue
				}
				for o := 0; o < nk && d > 0; o++ {
					if o != k && a[o][i] > 0 {
						t := min(d, a[o][i])
						a[o][i] -= t
						a[k][i] += t
						d -= t
						moved = true
					}
				}
			}
			if !moved {
				break
			}
		}
	}
	out := map[string]Spend{}
	for k, cd := range m.cards {
		s := Spend{}
		for i, cat := range m.cats {
			if a[k][i] > 0 {
				s[cat] = a[k][i]
			}
		}
		out[cd.ID] = s
	}
	return out
}

func realValue(cards []Card, alloc map[string]Spend, held bool) (net int64) {
	for _, c := range cards {
		net += NetYield(c, alloc[c.ID], held).NetPaise
	}
	return
}

// solveSubset runs the level-enumerating LP for exactly these cards (all of them are assumed in use).
func solveSubset(cards []Card, spend Spend, held bool) (map[string]Spend, bool) {
	m := buildModel(cards, spend)
	if len(m.cats) == 0 {
		out := map[string]Spend{}
		for _, c := range cards {
			out[c.ID] = Spend{}
		}
		return out, true
	}
	ts := make([][]threshold, len(cards))
	states := 1
	for k, c := range cards {
		ts[k] = thresholdsOf(c)
		if states *= len(ts[k]) + 1; states > maxLevelStates {
			return nil, false
		}
	}

	var best map[string]Spend
	var bestVal int64
	level := make([]int, len(cards))
	for s := 0; s < states; s++ {
		// odometer: card 0 is the most significant digit, so iteration order is deterministic
		rem := s
		for k := len(cards) - 1; k >= 0; k-- {
			level[k] = rem % (len(ts[k]) + 1)
			rem /= len(ts[k]) + 1
		}
		x, _, ok := simplex(m.c, m.withLevels(ts, level))
		if !ok {
			continue
		}
		alloc := m.toAlloc(x, ts, level)
		if v := realValue(cards, alloc, held); best == nil || v > bestVal {
			best, bestVal = alloc, v
		}
	}
	return best, best != nil
}

// maxSubsetCards bounds the subset enumeration (2^n - 1 LP families) used when unused cards pay no fee.
const maxSubsetCards = 3

// allocateExact returns ok=false when the problem is too large or every LP fails. A card nobody spends on
// pays no fee unless it is already held, so when held is false each non-empty subset of cards is tried too.
func allocateExact(cards []Card, spend Spend, held bool) (map[string]Spend, bool) {
	n := len(cards)
	masks := []int{1<<n - 1}
	if !held && n <= maxSubsetCards {
		masks = masks[:0]
		for mk := 1; mk < 1<<n; mk++ {
			masks = append(masks, mk)
		}
	}
	var best map[string]Spend
	var bestVal int64
	for _, mk := range masks {
		var sub []Card
		for k := 0; k < n; k++ {
			if mk>>k&1 == 1 {
				sub = append(sub, cards[k])
			}
		}
		alloc, ok := solveSubset(sub, spend, held)
		if !ok {
			continue
		}
		full := map[string]Spend{}
		for _, c := range cards {
			full[c.ID] = Spend{}
		}
		for id, sp := range alloc {
			full[id] = sp
		}
		if v := realValue(cards, full, held); best == nil || v > bestVal {
			best, bestVal = full, v
		}
	}
	return best, best != nil
}

// allocate is the best of the exact and greedy allocations by real value.
func allocate(cards []Card, spend Spend, held bool) map[string]Spend {
	best := allocateGreedy(cards, spend)
	bestVal := realValue(cards, best, held)
	if ex, ok := allocateExact(cards, spend, held); ok {
		if v := realValue(cards, ex, held); v > bestVal {
			best = ex
		}
	}
	return best
}

// upperBound bounds the real net of any allocation to these cards: the LP relaxation with no step
// constraints, plus every milestone bonus, and no fees. +Inf when the LP cannot be solved.
func upperBound(cards []Card, spend Spend) float64 {
	m := buildModel(cards, spend)
	var bonus float64
	for _, c := range cards {
		for _, ms := range c.Milestones {
			bonus += float64(ms.BonusValuePaise)
		}
	}
	if len(m.cats) == 0 {
		return bonus + 1
	}
	_, obj, ok := simplex(m.c, m.rows)
	if !ok {
		return math.Inf(1)
	}
	return obj + bonus + 1
}

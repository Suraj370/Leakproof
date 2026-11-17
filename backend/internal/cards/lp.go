package cards

// A small dense two-phase simplex (Bland's rule, so it cannot cycle). It maximises c.x subject to
// rows of the form a.x <= b or a.x >= b with x >= 0. Problems here have at most a few dozen variables.
//
// The arithmetic is written so that its results are bit-identical to the TypeScript port in
// frontend/src/lp.ts: every product is wrapped in float64() so a compiler may not fuse a multiply and an
// add (FMA), and operations run in the same order.

type rowKind int

const (
	rowLE rowKind = iota
	rowGE
)

type lpRow struct {
	coef []float64
	kind rowKind
	rhs  float64
}

const (
	lpEps      = 1e-9
	lpMaxIters = 5000
)

// simplex returns the optimal x, the optimal objective, and ok=false when the problem is infeasible,
// unbounded, or the iteration cap was hit (callers then fall back to the greedy allocator).
func simplex(c []float64, rows []lpRow) (x []float64, obj float64, ok bool) {
	n, m := len(c), len(rows)
	nArt := 0
	norm := make([]lpRow, m)
	for i, r := range rows {
		coef := append([]float64(nil), r.coef...)
		rhs, kind := r.rhs, r.kind
		if rhs < 0 { // keep every right-hand side >= 0 by flipping the row
			for j := range coef {
				coef[j] = -coef[j]
			}
			rhs = -rhs
			if kind == rowLE {
				kind = rowGE
			} else {
				kind = rowLE
			}
		}
		if kind == rowGE {
			nArt++
		}
		norm[i] = lpRow{coef, kind, rhs}
	}

	art := n + m // first artificial column
	cols := n + m + nArt
	T := make([][]float64, m+1)
	for i := range T {
		T[i] = make([]float64, cols+1)
	}
	basis := make([]int, m)
	ai := 0
	for i, r := range norm {
		copy(T[i][:n], r.coef)
		T[i][cols] = r.rhs
		if r.kind == rowLE {
			T[i][n+i] = 1
			basis[i] = n + i
		} else {
			T[i][n+i] = -1
			T[i][art+ai] = 1
			basis[i] = art + ai
			ai++
		}
	}

	pivot := func(pr, pc int) {
		p := T[pr][pc]
		for j := range T[pr] {
			T[pr][j] = T[pr][j] / p
		}
		for i := range T {
			if i == pr {
				continue
			}
			f := T[i][pc]
			if f == 0 {
				continue
			}
			for j := range T[i] {
				T[i][j] = T[i][j] - float64(f*T[pr][j])
			}
		}
		basis[pr] = pc
	}

	// run iterates Bland's rule over entering columns [0, allowed).
	run := func(allowed int) bool {
		for iter := 0; iter < lpMaxIters; iter++ {
			pc := -1
			for j := 0; j < allowed; j++ {
				if T[m][j] < -lpEps {
					pc = j
					break
				}
			}
			if pc < 0 {
				return true // optimal
			}
			pr := -1
			best := 0.0
			for i := 0; i < m; i++ {
				if T[i][pc] > lpEps {
					ratio := T[i][cols] / T[i][pc]
					if pr < 0 || ratio < best-lpEps || (ratio <= best+lpEps && basis[i] < basis[pr]) {
						pr, best = i, ratio
					}
				}
			}
			if pr < 0 {
				return false // unbounded
			}
			pivot(pr, pc)
		}
		return false
	}

	if nArt > 0 { // phase 1: maximise -(sum of artificials)
		for k := 0; k < nArt; k++ {
			T[m][art+k] = 1
		}
		for i := 0; i < m; i++ {
			if basis[i] >= art {
				for j := range T[m] {
					T[m][j] = T[m][j] - T[i][j]
				}
			}
		}
		if !run(cols) {
			return nil, 0, false
		}
		if T[m][cols] < -1e-7 {
			return nil, 0, false // infeasible
		}
		for i := 0; i < m; i++ { // drive zero-level artificials out of the basis
			if basis[i] < art {
				continue
			}
			for j := 0; j < art; j++ {
				if T[i][j] > lpEps || T[i][j] < -lpEps {
					pivot(i, j)
					break
				}
			}
		}
	}

	// phase 2: the real objective, expressed in terms of the current basis
	for j := range T[m] {
		T[m][j] = 0
	}
	for j := 0; j < n; j++ {
		T[m][j] = -c[j]
	}
	for i := 0; i < m; i++ {
		if b := basis[i]; b < n && c[b] != 0 {
			for j := range T[m] {
				T[m][j] = T[m][j] + float64(c[b]*T[i][j])
			}
		}
	}
	if !run(art) {
		return nil, 0, false
	}

	x = make([]float64, n)
	for i := 0; i < m; i++ {
		if basis[i] < n {
			x[basis[i]] = T[i][cols]
		}
	}
	return x, T[m][cols], true
}

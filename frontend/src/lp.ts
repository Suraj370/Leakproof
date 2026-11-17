// Dense two-phase simplex with Bland's rule. A line-for-line port of backend/internal/cards/lp.go: same
// operation order, so results are identical to the Go solver (see solver.test.ts / golden_solver.json).
// Maximises c.x subject to rows of the form a.x <= b or a.x >= b, with x >= 0.

export type RowKind = 'le' | 'ge';
export interface LpRow { coef: number[]; kind: RowKind; rhs: number }

const EPS = 1e-9;
const MAX_ITERS = 5000;

export interface LpResult { x: number[]; obj: number }

/** Returns null when infeasible, unbounded, or the iteration cap is hit. */
export function simplex(c: number[], rows: LpRow[]): LpResult | null {
  const n = c.length;
  const m = rows.length;
  let nArt = 0;
  const norm: LpRow[] = rows.map((r) => {
    let coef = [...r.coef];
    let rhs = r.rhs;
    let kind = r.kind;
    if (rhs < 0) { // keep every right-hand side >= 0 by flipping the row
      coef = coef.map((v) => -v);
      rhs = -rhs;
      kind = kind === 'le' ? 'ge' : 'le';
    }
    if (kind === 'ge') nArt++;
    return { coef, kind, rhs };
  });

  const art = n + m; // first artificial column
  const cols = n + m + nArt;
  const T: number[][] = Array.from({ length: m + 1 }, () => new Array<number>(cols + 1).fill(0));
  const basis = new Array<number>(m).fill(0);
  let ai = 0;
  norm.forEach((r, i) => {
    const row = T[i] as number[];
    r.coef.forEach((v, j) => { row[j] = v; });
    row[cols] = r.rhs;
    if (r.kind === 'le') {
      row[n + i] = 1;
      basis[i] = n + i;
    } else {
      row[n + i] = -1;
      row[art + ai] = 1;
      basis[i] = art + ai;
      ai++;
    }
  });

  const at = (i: number, j: number) => (T[i] as number[])[j] as number;

  const pivot = (pr: number, pc: number) => {
    const prow = T[pr] as number[];
    const p = prow[pc] as number;
    for (let j = 0; j < prow.length; j++) prow[j] = (prow[j] as number) / p;
    for (let i = 0; i < T.length; i++) {
      if (i === pr) continue;
      const row = T[i] as number[];
      const f = row[pc] as number;
      if (f === 0) continue;
      for (let j = 0; j < row.length; j++) row[j] = (row[j] as number) - f * (prow[j] as number);
    }
    basis[pr] = pc;
  };

  // iterate Bland's rule over entering columns [0, allowed)
  const run = (allowed: number): boolean => {
    for (let iter = 0; iter < MAX_ITERS; iter++) {
      let pc = -1;
      for (let j = 0; j < allowed; j++) {
        if (at(m, j) < -EPS) { pc = j; break; }
      }
      if (pc < 0) return true; // optimal
      let pr = -1;
      let best = 0;
      for (let i = 0; i < m; i++) {
        if (at(i, pc) > EPS) {
          const ratio = at(i, cols) / at(i, pc);
          if (pr < 0 || ratio < best - EPS || (ratio <= best + EPS && (basis[i] as number) < (basis[pr] as number))) {
            pr = i;
            best = ratio;
          }
        }
      }
      if (pr < 0) return false; // unbounded
      pivot(pr, pc);
    }
    return false;
  };

  const z = T[m] as number[];
  if (nArt > 0) { // phase 1: maximise -(sum of artificials)
    for (let k = 0; k < nArt; k++) z[art + k] = 1;
    for (let i = 0; i < m; i++) {
      if ((basis[i] as number) >= art) {
        const row = T[i] as number[];
        for (let j = 0; j < z.length; j++) z[j] = (z[j] as number) - (row[j] as number);
      }
    }
    if (!run(cols)) return null;
    if (at(m, cols) < -1e-7) return null; // infeasible
    for (let i = 0; i < m; i++) { // drive zero-level artificials out of the basis
      if ((basis[i] as number) < art) continue;
      for (let j = 0; j < art; j++) {
        const v = at(i, j);
        if (v > EPS || v < -EPS) { pivot(i, j); break; }
      }
    }
  }

  // phase 2: the real objective, expressed in terms of the current basis
  z.fill(0);
  for (let j = 0; j < n; j++) z[j] = -(c[j] as number);
  for (let i = 0; i < m; i++) {
    const b = basis[i] as number;
    if (b < n && (c[b] as number) !== 0) {
      const row = T[i] as number[];
      const cb = c[b] as number;
      for (let j = 0; j < z.length; j++) z[j] = (z[j] as number) + cb * (row[j] as number);
    }
  }
  if (!run(art)) return null;

  const x = new Array<number>(n).fill(0);
  for (let i = 0; i < m; i++) {
    const b = basis[i] as number;
    if (b < n) x[b] = at(i, cols);
  }
  return { x, obj: at(m, cols) };
}

// Exact allocation of a monthly spend profile across cards. Port of backend/internal/cards/solve.go: for
// every combination of milestone / fee-waiver "levels" it solves an LP, keeps the allocation with the best
// REAL value (netYield, with its per-category point flooring), and falls back to the greedy allocator when
// the state space is too big. Optimal up to that flooring, and never worse than greedy.
import type { Category, CreditCard, Paise, Spend } from './contracts';
import { simplex, type LpRow } from './lp';
import { allocateGreedy, netYield, realValue, ruleFor } from './rewards';

const MAX_LEVEL_STATES = 729;
const MAX_SUBSET_CARDS = 3;

interface Threshold { spendPaise: number; bonusPaise: number }

/** A card's yearly-eligible-spend steps: milestones plus the fee waiver (worth the fee). */
function thresholdsOf(c: CreditCard): Threshold[] {
  const ts: Threshold[] = (c.milestones ?? []).map((m) => ({ spendPaise: m.annualSpendPaise, bonusPaise: m.bonusValuePaise }));
  if (c.annualFeePaise > 0 && c.feeWaiverSpendPaise > 0) {
    ts.push({ spendPaise: c.feeWaiverSpendPaise, bonusPaise: c.annualFeePaise });
  }
  return ts.sort((a, b) => a.spendPaise - b.spendPaise); // stable, like Go's SliceStable
}

interface Model {
  cards: CreditCard[];
  cats: Category[];
  spend: number[]; // rupees per month
  paise: number[];
  xi: number[][]; // [card][cat] -> variable index, or -1 when the card earns nothing there
  nvars: number;
  c: number[];
  rows: LpRow[];
}

const hasCap = (c: CreditCard, group: string) => group !== '' && (c.groupCapPoints ?? {})[group] !== undefined;

function buildModel(cards: CreditCard[], spend: Spend): Model {
  const cats = (Object.keys(spend) as Category[])
    .filter((k) => (spend[k] ?? 0) > 0 && k !== 'EXCLUDED')
    .sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
  const paise = cats.map((k) => spend[k] as number);
  const sp = paise.map((p) => p / 100);

  let nvars = 0;
  const xi: number[][] = cards.map((cd) =>
    cats.map((cat) => (ruleFor(cd, cat).excluded ? -1 : nvars++)));

  const ps: { card: number; group: string; idx: number }[] = [];
  cards.forEach((cd, k) => {
    const seen = new Set<string>();
    const groups: string[] = [];
    cats.forEach((cat, i) => {
      if ((xi[k] as number[])[i] as number < 0) return;
      const g = ruleFor(cd, cat).capGroup;
      if (hasCap(cd, g) && !seen.has(g)) { seen.add(g); groups.push(g); }
    });
    groups.sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
    for (const g of groups) ps.push({ card: k, group: g, idx: nvars++ });
  });

  const c = new Array<number>(nvars).fill(0);
  cards.forEach((cd, k) => {
    cats.forEach((cat, i) => {
      const j = (xi[k] as number[])[i] as number;
      if (j < 0) return;
      const r = ruleFor(cd, cat);
      if (hasCap(cd, r.capGroup)) return; // reward flows through the points variable
      c[j] = (12 * cd.redeemPaisePerPoint * r.earnBps) / 10000;
    });
  });

  const zero = () => new Array<number>(nvars).fill(0);
  const rows: LpRow[] = [];
  cats.forEach((_, i) => { // each category's spend is shared between the cards
    const row = zero();
    cards.forEach((__, k) => {
      const j = (xi[k] as number[])[i] as number;
      if (j >= 0) row[j] = 1;
    });
    rows.push({ coef: row, kind: 'le', rhs: sp[i] as number });
  });
  for (const p of ps) { // points in a capped group: p <= cap and p <= sum(earn * x)
    const cd = cards[p.card] as CreditCard;
    c[p.idx] = 12 * cd.redeemPaisePerPoint;
    const capRow = zero();
    capRow[p.idx] = 1;
    rows.push({ coef: capRow, kind: 'le', rhs: cd.groupCapPoints[p.group] as number });

    const link = zero();
    link[p.idx] = 1;
    cats.forEach((cat, i) => {
      const j = (xi[p.card] as number[])[i] as number;
      if (j >= 0 && ruleFor(cd, cat).capGroup === p.group) link[j] = -(ruleFor(cd, cat).earnBps / 10000);
    });
    rows.push({ coef: link, kind: 'le', rhs: 0 });
  }
  return { cards, cats, spend: sp, paise, xi, nvars, c, rows };
}

/** For every card at level l>0: yearly eligible spend must reach threshold l. */
function withLevels(m: Model, ts: Threshold[][], level: number[]): LpRow[] {
  const rows = [...m.rows];
  level.forEach((l, k) => {
    if (l === 0) return;
    const row = new Array<number>(m.nvars).fill(0);
    m.cats.forEach((_, i) => {
      const j = (m.xi[k] as number[])[i] as number;
      if (j >= 0) row[j] = 1;
    });
    rows.push({ coef: row, kind: 'ge', rhs: ((ts[k] as Threshold[])[l - 1] as Threshold).spendPaise / 1200 });
  });
  return rows;
}

/** Rounds an LP solution to whole paise, never over-assigning a category, and nudges cards that must reach a step. */
function toAlloc(m: Model, x: number[], ts: Threshold[][], level: number[]): Record<string, Spend> {
  const nk = m.cards.length;
  const ni = m.cats.length;
  const a: number[][] = m.cards.map((_, k) =>
    m.cats.map((__, i) => {
      const j = (m.xi[k] as number[])[i] as number;
      return j >= 0 ? Math.max(0, Math.floor((x[j] as number) * 100 + 0.5)) : 0;
    }));
  const cell = (k: number, i: number) => (a[k] as number[])[i] as number;
  const add = (k: number, i: number, d: number) => { (a[k] as number[])[i] = cell(k, i) + d; };
  const used = (i: number) => { let s = 0; for (let k = 0; k < nk; k++) s += cell(k, i); return s; };

  for (let i = 0; i < ni; i++) {
    while (used(i) > (m.paise[i] as number)) { // rounding overshoot: trim the largest holder
      let big = 0;
      for (let k = 1; k < nk; k++) if (cell(k, i) > cell(big, i)) big = k;
      add(big, i, -1);
    }
  }
  level.forEach((l, k) => {
    if (l === 0) return;
    const need = Math.floor((((ts[k] as Threshold[])[l - 1] as Threshold).spendPaise + 11) / 12); // 12x >= threshold
    let have = 0;
    for (let i = 0; i < ni; i++) have += cell(k, i);
    for (let d = need - have; d > 0;) {
      let moved = false;
      for (let i = 0; i < ni && d > 0; i++) {
        if (((m.xi[k] as number[])[i] as number) < 0) continue;
        const spare = (m.paise[i] as number) - used(i);
        if (spare > 0) {
          const t = Math.min(d, spare);
          add(k, i, t);
          d -= t;
          moved = true;
          continue;
        }
        for (let o = 0; o < nk && d > 0; o++) {
          if (o !== k && cell(o, i) > 0) {
            const t = Math.min(d, cell(o, i));
            add(o, i, -t);
            add(k, i, t);
            d -= t;
            moved = true;
          }
        }
      }
      if (!moved) break;
    }
  });

  const out: Record<string, Spend> = {};
  m.cards.forEach((cd, k) => {
    const s: Spend = {};
    m.cats.forEach((cat, i) => { if (cell(k, i) > 0) s[cat] = cell(k, i); });
    out[cd.id] = s;
  });
  return out;
}

/** The level-enumerating LP for exactly these cards (all assumed in use). */
function solveSubset(cards: CreditCard[], spend: Spend, held: boolean): Record<string, Spend> | null {
  const m = buildModel(cards, spend);
  if (m.cats.length === 0) {
    const out: Record<string, Spend> = {};
    for (const c of cards) out[c.id] = {};
    return out;
  }
  const ts = cards.map(thresholdsOf);
  let states = 1;
  for (const t of ts) {
    states *= t.length + 1;
    if (states > MAX_LEVEL_STATES) return null;
  }

  let best: Record<string, Spend> | null = null;
  let bestVal = 0;
  const level = new Array<number>(cards.length).fill(0);
  for (let s = 0; s < states; s++) {
    // odometer: card 0 is the most significant digit, so iteration order is deterministic
    let rem = s;
    for (let k = cards.length - 1; k >= 0; k--) {
      const base = (ts[k] as Threshold[]).length + 1;
      level[k] = rem % base;
      rem = Math.floor(rem / base);
    }
    const sol = simplex(m.c, withLevels(m, ts, level));
    if (!sol) continue;
    const alloc = toAlloc(m, sol.x, ts, level);
    const v = realValue(cards, alloc, held);
    if (best === null || v > bestVal) { best = alloc; bestVal = v; }
  }
  return best;
}

/** Null when the problem is too large or every LP fails. When unused cards pay no fee, subsets are tried too. */
export function allocateExact(cards: CreditCard[], spend: Spend, held: boolean): Record<string, Spend> | null {
  const n = cards.length;
  let masks = [2 ** n - 1];
  if (!held && n <= MAX_SUBSET_CARDS) {
    masks = [];
    for (let mk = 1; mk < 2 ** n; mk++) masks.push(mk);
  }
  let best: Record<string, Spend> | null = null;
  let bestVal = 0;
  for (const mk of masks) {
    const sub = cards.filter((_, k) => Math.floor(mk / 2 ** k) % 2 === 1);
    const alloc = solveSubset(sub, spend, held);
    if (!alloc) continue;
    const full: Record<string, Spend> = {};
    for (const c of cards) full[c.id] = {};
    for (const [id, sp] of Object.entries(alloc)) full[id] = sp;
    const v = realValue(cards, full, held);
    if (best === null || v > bestVal) { best = full; bestVal = v; }
  }
  return best;
}

/** The better of the exact and greedy allocations by real value. */
export function allocate(cards: CreditCard[], spend: Spend, held = false): Record<string, Spend> {
  let best = allocateGreedy(cards, spend);
  const bestVal = realValue(cards, best, held);
  const ex = allocateExact(cards, spend, held);
  if (ex && realValue(cards, ex, held) > bestVal) best = ex;
  return best;
}

/** Bounds the real net of any allocation: LP relaxation without steps, plus every milestone, minus no fees. */
export function upperBound(cards: CreditCard[], spend: Spend): number {
  const m = buildModel(cards, spend);
  let bonus = 0;
  for (const c of cards) for (const ms of c.milestones ?? []) bonus += ms.bonusValuePaise;
  if (m.cats.length === 0) return bonus + 1;
  const sol = simplex(m.c, m.rows);
  return sol ? sol.obj + bonus + 1 : Infinity;
}

export function evaluate(cards: CreditCard[], spend: Spend, held: boolean) {
  const alloc = allocate(cards, spend, held);
  const yields = cards.map((c) => netYield(c, alloc[c.id] ?? {}, held));
  return { yields, netPaise: yields.reduce((s, y) => s + y.netPaise, 0), alloc };
}

export interface ComboResult {
  cards: CreditCard[];
  netPaise: Paise;
  baselinePaise: Paise;
  gainPaise: Paise;
}

/**
 * Best 1- or 2-card combination of the catalog versus the wallet. Every combination gets an LP upper
 * bound; they are solved exactly in descending bound order and the search stops once the next bound cannot
 * beat the best exact value. Ties go to the combination first in catalog order.
 */
export function bestCombo(catalog: CreditCard[], wallet: CreditCard[], spend: Spend): ComboResult {
  const baselinePaise = evaluate(wallet, spend, true).netPaise;
  if (catalog.length === 0) return { cards: [], netPaise: baselinePaise, baselinePaise, gainPaise: 0 };

  const cands: { cards: CreditCard[]; ub: number; order: number }[] = [];
  const add = (cards: CreditCard[]) => cands.push({ cards, ub: upperBound(cards, spend), order: cands.length });
  catalog.forEach((a, i) => {
    add([a]);
    for (const b of catalog.slice(i + 1)) add([a, b]);
  });
  cands.sort((x, y) => y.ub - x.ub); // stable

  let bestOrder = -1;
  let bestNet = 0;
  let bestCards: CreditCard[] = [];
  for (const c of cands) {
    if (bestOrder >= 0 && c.ub < bestNet) break; // nothing left can beat the best exact value
    const { netPaise } = evaluate(c.cards, spend, false);
    if (bestOrder < 0 || netPaise > bestNet || (netPaise === bestNet && c.order < bestOrder)) {
      bestOrder = c.order;
      bestNet = netPaise;
      bestCards = c.cards;
    }
  }
  return { cards: bestCards, netPaise: bestNet, baselinePaise, gainPaise: bestNet - baselinePaise };
}

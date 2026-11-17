// Client-side card engine: an integer-only port of backend/internal/cards. Zero API latency for sliders.
// engine.test.ts asserts the same golden fixture as the Go tests so the two can never drift.
import type { Category, CardYield, CreditCard, Spend, Paise, CategoryRule } from './contracts';

const ruleFor = (c: CreditCard, cat: Category): CategoryRule =>
  c.rules[cat] ?? { earnBps: c.baseEarnBps, excluded: false, capGroup: '' };

const floorDiv = (n: number, d: number) => (n - (n % d)) / d; // exact for safe integers

function grant(c: CreditCard, cat: Category, spend: Paise, used: Record<string, number>) {
  const r = ruleFor(c, cat);
  if (cat === 'EXCLUDED' || r.excluded) return { pts: 0, raw: 0 };
  const raw = floorDiv(spend * r.earnBps, 1_000_000);
  const cap = r.capGroup ? c.groupCapPoints[r.capGroup] : undefined;
  if (cap === undefined) return { pts: raw, raw };
  return { pts: Math.min(raw, Math.max(0, cap - (used[r.capGroup] ?? 0))), raw };
}

function positiveCats(spend: Spend, cmp: (a: Category, b: Category) => number): Category[] {
  return (Object.keys(spend) as Category[])
    .filter((k) => (spend[k] ?? 0) > 0)
    .sort((a, b) => cmp(a, b) || (a < b ? -1 : a > b ? 1 : 0)); // deterministic tie-break, same as Go
}

export function netYield(c: CreditCard, monthly: Spend, holdFee = false): CardYield {
  const cats = positiveCats(monthly, (a, b) => ruleFor(c, b).earnBps - ruleFor(c, a).earnBps);
  const used: Record<string, number> = {};
  let pts = 0, raw = 0, eligible = 0, total = 0;
  for (const cat of cats) {
    const s = monthly[cat] ?? 0;
    total += s;
    const g = grant(c, cat, s, used);
    pts += g.pts;
    raw += g.raw;
    const r = ruleFor(c, cat);
    if (r.capGroup) used[r.capGroup] = (used[r.capGroup] ?? 0) + g.pts;
    if (cat !== 'EXCLUDED' && !r.excluded) eligible += s;
  }
  const annualRewardPaise = pts * c.redeemPaisePerPoint * 12;
  const waived = c.feeWaiverSpendPaise > 0 && eligible * 12 >= c.feeWaiverSpendPaise;
  const feePaise = (total > 0 || holdFee) && !waived ? c.annualFeePaise : 0;
  const milestoneBonusPaise = c.milestones.reduce((s, m) => s + (eligible * 12 >= m.annualSpendPaise ? m.bonusValuePaise : 0), 0);
  return {
    cardId: c.id,
    annualRewardPaise,
    lostToCapsPaise: (raw - pts) * c.redeemPaisePerPoint * 12,
    milestoneBonusPaise,
    feePaise,
    netPaise: annualRewardPaise + milestoneBonusPaise - feePaise,
  };
}

export function allocate(cards: CreditCard[], spend: Spend, chunk = 50_000): Record<string, Spend> {
  const out: Record<string, Spend> = {};
  const used: Record<string, Record<string, number>> = {};
  for (const c of cards) { out[c.id] = {}; used[c.id] = {}; }
  if (cards.length === 0) return out;

  const bestRate = (cat: Category) =>
    Math.max(0, ...cards.map((c) => ruleFor(c, cat).earnBps * c.redeemPaisePerPoint));

  for (const cat of positiveCats(spend, (a, b) => bestRate(b) - bestRate(a))) {
    let left = spend[cat] ?? 0;
    while (left > 0) {
      const part = Math.min(chunk, left);
      let pick = cards[0] as CreditCard;
      let pickVal = -1;
      for (const c of cards) {
        const v = grant(c, cat, part, used[c.id] ?? {}).pts * c.redeemPaisePerPoint;
        if (v > pickVal) { pick = c; pickVal = v; }
      }
      const u = (used[pick.id] ??= {});
      const g = grant(pick, cat, part, u);
      const grp = ruleFor(pick, cat).capGroup;
      if (grp) u[grp] = (u[grp] ?? 0) + g.pts;
      const mine = (out[pick.id] ??= {});
      mine[cat] = (mine[cat] ?? 0) + part;
      left -= part;
    }
  }
  return out;
}

export function evaluate(cards: CreditCard[], spend: Spend, held: boolean) {
  const alloc = allocate(cards, spend);
  const yields = cards.map((c) => netYield(c, alloc[c.id] ?? {}, held));
  return { yields, netPaise: yields.reduce((s, y) => s + y.netPaise, 0), alloc };
}

export interface ComboResult {
  cards: CreditCard[];
  netPaise: Paise;
  baselinePaise: Paise;
  gainPaise: Paise;
}

export function bestCombo(catalog: CreditCard[], wallet: CreditCard[], spend: Spend): ComboResult {
  const baselinePaise = evaluate(wallet, spend, true).netPaise;
  if (catalog.length === 0) return { cards: [], netPaise: baselinePaise, baselinePaise, gainPaise: 0 };
  let best: { cards: CreditCard[]; netPaise: number } = { cards: [], netPaise: -Infinity };
  const consider = (cards: CreditCard[]) => {
    const { netPaise } = evaluate(cards, spend, false);
    if (netPaise > best.netPaise) best = { cards, netPaise };
  };
  catalog.forEach((a, i) => {
    consider([a]);
    for (const b of catalog.slice(i + 1)) consider([a, b]);
  });
  return { ...best, baselinePaise, gainPaise: best.netPaise - baselinePaise };
}

export const totalMonthly = (spend: Spend) => Object.values(spend).reduce((a, b) => a + (b ?? 0), 0);

/** Reward yield in bps of total annual spend (rupees back per 100 spent, x100). */
export function yieldBps(netPaise: Paise, spend: Spend): number {
  const annual = totalMonthly(spend) * 12;
  return annual > 0 ? Math.round((netPaise * 10_000) / annual) : 0;
}

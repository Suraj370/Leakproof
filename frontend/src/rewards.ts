// Reward maths for ONE card (caps, exclusions, redemption value, fee waivers, milestones) and the greedy
// allocator. Integer-only port of backend/internal/cards/cards.go.
import type { CardYield, Category, CategoryRule, CreditCard, Paise, Spend } from './contracts';

export const ruleFor = (c: CreditCard, cat: Category): CategoryRule =>
  (c.rules ?? {})[cat] ?? { earnBps: c.baseEarnBps, excluded: false, capGroup: '' };

const floorDiv = (n: number, d: number) => (n - (n % d)) / d; // exact for safe integers

export function grant(c: CreditCard, cat: Category, spend: Paise, used: Record<string, number>) {
  const r = ruleFor(c, cat);
  if (cat === 'EXCLUDED' || r.excluded) return { pts: 0, raw: 0 };
  const raw = floorDiv(spend * r.earnBps, 1_000_000);
  const cap = r.capGroup ? (c.groupCapPoints ?? {})[r.capGroup] : undefined;
  if (cap === undefined) return { pts: raw, raw };
  return { pts: Math.min(raw, Math.max(0, cap - (used[r.capGroup] ?? 0))), raw };
}

export function positiveCats(spend: Spend, cmp: (a: Category, b: Category) => number): Category[] {
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
  const milestoneBonusPaise = (c.milestones ?? []).reduce(
    (s, m) => s + (eligible * 12 >= m.annualSpendPaise ? m.bonusValuePaise : 0), 0);
  return {
    cardId: c.id,
    annualRewardPaise,
    lostToCapsPaise: (raw - pts) * c.redeemPaisePerPoint * 12,
    milestoneBonusPaise,
    feePaise,
    netPaise: annualRewardPaise + milestoneBonusPaise - feePaise,
  };
}

export function realValue(cards: CreditCard[], alloc: Record<string, Spend>, held: boolean): number {
  return cards.reduce((s, c) => s + netYield(c, alloc[c.id] ?? {}, held).netPaise, 0);
}

/**
 * Splits each category's spend across cards in Rs500 chunks, always to the card with the best marginal
 * reward given its remaining cap. A heuristic (ignores milestones, can misuse a shared cap): the exact
 * solver in solver.ts runs it only as a safety net and as the fallback.
 */
export function allocateGreedy(cards: CreditCard[], spend: Spend, chunk = 50_000): Record<string, Spend> {
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

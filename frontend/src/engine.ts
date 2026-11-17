// Client-side card engine: the same maths as backend/internal/cards, so sliders react with zero API latency.
// engine.test.ts and solver.test.ts assert shared golden fixtures so Go and TypeScript can never drift.
import type { Paise, Spend } from './contracts';

export { netYield, allocateGreedy } from './rewards';
export { allocate, allocateExact, bestCombo, evaluate, upperBound, type ComboResult } from './solver';

export const totalMonthly = (spend: Spend) => Object.values(spend).reduce((a, b) => a + (b ?? 0), 0);

/** Reward yield in bps of total annual spend (rupees back per 100 spent, x100). */
export function yieldBps(netPaise: Paise, spend: Spend): number {
  const annual = totalMonthly(spend) * 12;
  return annual > 0 ? Math.round((netPaise * 10_000) / annual) : 0;
}

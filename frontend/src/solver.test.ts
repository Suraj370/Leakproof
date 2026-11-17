import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import type { CreditCard, Spend } from './contracts';
import { simplex } from './lp';
import { allocateGreedy, netYield, realValue } from './rewards';
import { allocate, allocateExact, bestCombo, upperBound } from './solver';

interface AllocCase {
  name: string; cards: CreditCard[]; monthlySpendPaise: Spend; held: boolean;
  expectedAllocation: Record<string, Spend>; expectedNetPaise: number;
}
interface ComboCase {
  name: string; catalog: CreditCard[]; current: CreditCard[]; monthlySpendPaise: Spend;
  expectedCardIds: string[]; expectedNetPaise: number; expectedBaselinePaise: number;
}
const golden: { allocations: AllocCase[]; combos: ComboCase[] } = JSON.parse(
  readFileSync(resolve(process.cwd(), '../backend/testdata/golden_solver.json'), 'utf8'), // cwd = frontend/
);

const near = (a: number, b: number) => Math.abs(a - b) < 1e-6;

describe('Go/TS parity: the exact solver (golden_solver.json)', () => {
  it('has the scenarios', () => {
    expect(golden.allocations.length).toBeGreaterThanOrEqual(11);
    expect(golden.combos.length).toBeGreaterThanOrEqual(4);
  });

  for (const c of golden.allocations) {
    it(`allocation: ${c.name}`, () => {
      const alloc = allocate(c.cards, c.monthlySpendPaise, c.held);
      expect(alloc).toEqual(c.expectedAllocation);
      expect(realValue(c.cards, alloc, c.held)).toBe(c.expectedNetPaise);
    });
  }

  for (const c of golden.combos) {
    it(`best combo: ${c.name}`, () => {
      const r = bestCombo(c.catalog, c.current, c.monthlySpendPaise);
      expect(r.cards.map((x) => x.id)).toEqual(c.expectedCardIds);
      expect(r.netPaise).toBe(c.expectedNetPaise);
      expect(r.baselinePaise).toBe(c.expectedBaselinePaise);
    });
  }
});

describe('simplex', () => {
  it('solves a textbook maximisation', () => {
    const r = simplex([3, 2], [
      { coef: [1, 1], kind: 'le', rhs: 4 }, { coef: [1, 3], kind: 'le', rhs: 6 }, { coef: [1, 0], kind: 'le', rhs: 3 },
    ]);
    expect(r && near(r.obj, 11) && near(r.x[0] ?? NaN, 3) && near(r.x[1] ?? NaN, 1)).toBe(true);
  });
  it('needs phase 1 for >= rows', () => {
    const r = simplex([-1, -1], [{ coef: [1, 2], kind: 'ge', rhs: 4 }, { coef: [3, 1], kind: 'ge', rhs: 3 }]);
    expect(r && near(r.obj, -2.2) && near(r.x[0] ?? NaN, 0.4) && near(r.x[1] ?? NaN, 1.8)).toBe(true);
  });
  it('flips negative right-hand sides', () => {
    const r = simplex([1, 1], [
      { coef: [1, 1], kind: 'le', rhs: 10 }, { coef: [-1, 0], kind: 'le', rhs: -2 }, { coef: [0, 1], kind: 'le', rhs: 3 },
    ]);
    expect(r && near(r.obj, 10)).toBe(true);
  });
  it('reports infeasible and unbounded problems', () => {
    expect(simplex([1], [{ coef: [1], kind: 'le', rhs: 1 }, { coef: [1], kind: 'ge', rhs: 2 }])).toBeNull();
    expect(simplex([1], [{ coef: [1], kind: 'ge', rhs: 1 }])).toBeNull();
  });
  it('terminates on degenerate, redundant rows', () => {
    const r = simplex([2, 1], [
      { coef: [1, 1], kind: 'le', rhs: 2 }, { coef: [1, 1], kind: 'le', rhs: 2 },
      { coef: [1, 0], kind: 'le', rhs: 0 }, { coef: [0, 1], kind: 'le', rhs: 5 },
    ]);
    expect(r && near(r.obj, 2)).toBe(true);
  });
});

const card = (over: Partial<CreditCard> & { id: string }): CreditCard => ({
  name: over.id, annualFeePaise: 0, feeWaiverSpendPaise: 0, baseEarnBps: 0, redeemPaisePerPoint: 100,
  rules: {}, groupCapPoints: {}, milestones: [], ...over,
});

describe('solver behaviour', () => {
  it('puts online on B and dining on A when A has one cap shared by both (greedy burns the cap)', () => {
    const a = card({
      id: 'A',
      rules: { ONLINE_SHOPPING: { earnBps: 500, excluded: false, capGroup: 'g' }, DINING: { earnBps: 400, excluded: false, capGroup: 'g' } },
      groupCapPoints: { g: 2000 },
    });
    const b = card({
      id: 'B',
      rules: { ONLINE_SHOPPING: { earnBps: 500, excluded: false, capGroup: '' }, DINING: { earnBps: 100, excluded: false, capGroup: '' } },
    });
    const spend: Spend = { ONLINE_SHOPPING: 4_000_000, DINING: 4_000_000 };
    expect(realValue([a, b], allocateGreedy([a, b], spend), false)).toBe(2_880_000);
    const alloc = allocate([a, b], spend);
    expect(realValue([a, b], alloc, false)).toBe(4_320_000);
    expect(alloc.A).toEqual({ DINING: 4_000_000 });
    expect(alloc.B).toEqual({ ONLINE_SHOPPING: 4_000_000 });
  });

  it('gives a milestone card just enough spend to unlock its bonus', () => {
    const m = card({ id: 'M', baseEarnBps: 100, milestones: [{ annualSpendPaise: 10_000_000, bonusValuePaise: 500_000 }] });
    const n = card({ id: 'N', baseEarnBps: 120 });
    const alloc = allocate([m, n], { OFFLINE_POS: 1_000_000 });
    const got = alloc.M?.OFFLINE_POS ?? 0;
    expect(got * 12).toBeGreaterThanOrEqual(10_000_000);
    expect(got).toBeLessThan(1_000_000);
    expect(netYield(m, alloc.M ?? {}).milestoneBonusPaise).toBe(500_000);
  });

  it('leaves a fee-heavy card unused when it only loses money', () => {
    const bad = card({ id: 'BAD', baseEarnBps: 50, annualFeePaise: 1_000_000 });
    const good = card({ id: 'GOOD', baseEarnBps: 150 });
    const alloc = allocateExact([bad, good], { DINING: 500_000 }, false);
    expect(alloc?.BAD).toEqual({});
    expect(alloc?.GOOD).toEqual({ DINING: 500_000 });
  });

  it('never assigns more than the spend, and is never below greedy or above the upper bound', () => {
    for (const c of golden.allocations) {
      const alloc = allocate(c.cards, c.monthlySpendPaise, c.held);
      const used: Spend = {};
      for (const s of Object.values(alloc)) for (const [k, v] of Object.entries(s)) {
        used[k as keyof Spend] = (used[k as keyof Spend] ?? 0) + v;
      }
      for (const [k, v] of Object.entries(used)) expect(v).toBeLessThanOrEqual(c.monthlySpendPaise[k as keyof Spend] ?? 0);
      expect(realValue(c.cards, alloc, c.held)).toBeGreaterThanOrEqual(realValue(c.cards, allocateGreedy(c.cards, c.monthlySpendPaise), c.held));
      expect(realValue(c.cards, alloc, c.held)).toBeLessThanOrEqual(upperBound(c.cards, c.monthlySpendPaise));
    }
  });

  it('falls back to greedy when the state space is huge', () => {
    const cards = ['A', 'B', 'C', 'D'].map((id, k) => card({
      id, baseEarnBps: 100 + k * 10, annualFeePaise: 100_000, feeWaiverSpendPaise: 5_000_000,
      milestones: [1, 2, 3, 4, 5, 6].map((m) => ({ annualSpendPaise: m * 10_000_000, bonusValuePaise: 10_000 })),
    }));
    expect(allocateExact(cards, { DINING: 2_000_000 }, false)).toBeNull();
    const alloc = allocate(cards, { DINING: 2_000_000 });
    const total = Object.values(alloc).reduce((s, x) => s + (x.DINING ?? 0), 0);
    expect(total).toBe(2_000_000);
  });
});

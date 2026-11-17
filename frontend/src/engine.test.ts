import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import type { CardYield, CreditCard, Spend } from './contracts';
import { allocate, bestCombo, evaluate, netYield, yieldBps } from './engine';

interface Golden {
  cards: CreditCard[];
  monthlySpendPaise: Spend;
  expectedAllocation: Record<string, Spend>;
  expectedYields: CardYield[];
}

const golden: Golden = JSON.parse(
  readFileSync(resolve(process.cwd(), '../backend/testdata/golden_cards.json'), 'utf8'), // cwd = frontend/
);

const cashback = (fee = 0, waiver = 0): CreditCard => ({
  id: 'cb', name: 'cb', annualFeePaise: fee, feeWaiverSpendPaise: waiver, baseEarnBps: 100, redeemPaisePerPoint: 100,
  rules: { ONLINE_SHOPPING: { earnBps: 500, excluded: false, capGroup: 'online' }, FUEL: { earnBps: 0, excluded: true, capGroup: '' } },
  groupCapPoints: { online: 5000 }, milestones: [],
});

describe('Go/TS parity (shared golden fixture)', () => {
  it('allocates identically to the Go solver', () => {
    expect(allocate(golden.cards, golden.monthlySpendPaise)).toEqual(golden.expectedAllocation);
  });
  it('yields identically to the Go calculator', () => {
    expect(evaluate(golden.cards, golden.monthlySpendPaise, false).yields).toEqual(golden.expectedYields);
  });
});

describe('netYield', () => {
  it('applies the monthly cap and reports rewards lost to it', () => {
    const y = netYield(cashback(), { ONLINE_SHOPPING: 20_000_000 });
    expect(y.annualRewardPaise).toBe(6_000_000);
    expect(y.lostToCapsPaise).toBe(6_000_000);
  });
  it('gives nothing for excluded categories', () => {
    expect(netYield(cashback(), { FUEL: 5_000_000, EXCLUDED: 10_000_000 }).annualRewardPaise).toBe(0);
  });
  it('waives the fee only on eligible spend', () => {
    const c = cashback(59_900, 20_000_000);
    expect(netYield(c, { ONLINE_SHOPPING: 10_000_000 }).feePaise).toBe(0);
    expect(netYield(c, { FUEL: 50_000_000 }).feePaise).toBe(59_900);
    expect(netYield(c, { ONLINE_SHOPPING: 1_500_000 }).feePaise).toBe(59_900);
  });
  it('charges the fee on a held but unused card', () => {
    expect(netYield(cashback(59_900), {}, true).netPaise).toBe(-59_900);
    expect(netYield(cashback(59_900), {}, false).feePaise).toBe(0);
  });
  it('scales by redemption value', () => {
    const c: CreditCard = { id: 'rp', name: 'rp', annualFeePaise: 0, feeWaiverSpendPaise: 0, baseEarnBps: 333,
      redeemPaisePerPoint: 25, rules: {}, groupCapPoints: {}, milestones: [] };
    expect(netYield(c, { OFFLINE_POS: 10_000_000 }).annualRewardPaise).toBe(3330 * 25 * 12);
  });
  it('spends a shared cap on the higher-rate category first', () => {
    const c: CreditCard = { id: 's', name: 's', annualFeePaise: 0, feeWaiverSpendPaise: 0, baseEarnBps: 0,
      redeemPaisePerPoint: 100,
      rules: { DINING: { earnBps: 500, excluded: false, capGroup: 'g' }, ONLINE_SHOPPING: { earnBps: 200, excluded: false, capGroup: 'g' } },
      groupCapPoints: { g: 1000 }, milestones: [] };
    expect(netYield(c, { DINING: 2_000_000, ONLINE_SHOPPING: 2_000_000 }).annualRewardPaise).toBe(1000 * 100 * 12);
  });
});

describe('bestCombo / yieldBps', () => {
  const a = { ...cashback(), id: 'A' };
  const b: CreditCard = { id: 'B', name: 'B', annualFeePaise: 0, feeWaiverSpendPaise: 0, baseEarnBps: 150,
    redeemPaisePerPoint: 100, rules: {}, groupCapPoints: {}, milestones: [] };
  const bad: CreditCard = { id: 'BAD', name: 'BAD', annualFeePaise: 1_000_000, feeWaiverSpendPaise: 0, baseEarnBps: 50,
    redeemPaisePerPoint: 100, rules: {}, groupCapPoints: {}, milestones: [] };
  const spend: Spend = { ONLINE_SHOPPING: 20_000_000, DINING: 2_000_000 };

  it('picks A+B and reports a positive gain over the current wallet', () => {
    const r = bestCombo([a, b, bad], [bad], spend);
    expect(r.cards.map((c) => c.id)).toEqual(['A', 'B']);
    expect(r.gainPaise).toBeGreaterThan(0);
    expect(r.gainPaise).toBe(r.netPaise - r.baselinePaise);
  });
  it('handles an empty catalog', () => {
    expect(bestCombo([], [], spend).gainPaise).toBe(0);
  });
  it('computes reward yield in bps of annual spend', () => {
    // Rs1,000/month spend, Rs180/yr net => 1.5% => 150 bps
    expect(yieldBps(18_000, { OFFLINE_POS: 100_000 })).toBe(150);
    expect(yieldBps(100, {})).toBe(0);
  });
});

describe('milestones', () => {
  const ms = (c: CreditCard, ...m: CreditCard['milestones']): CreditCard => ({ ...c, milestones: m });
  const card = ms(cashback(),
    { annualSpendPaise: 100_000_000, bonusValuePaise: 200_000 },
    { annualSpendPaise: 200_000_000, bonusValuePaise: 500_000 });

  it('pays each milestone once eligible annual spend reaches it (cumulative)', () => {
    expect(netYield(card, { ONLINE_SHOPPING: 10_000_000 }).milestoneBonusPaise).toBe(200_000);
    expect(netYield(card, { ONLINE_SHOPPING: 20_000_000 }).milestoneBonusPaise).toBe(700_000);
  });
  it('counts the threshold itself (>=) and nothing below it', () => {
    expect(netYield(card, { OFFLINE_POS: 8_333_333 }).milestoneBonusPaise).toBe(0); // 99,999,996 a year
    expect(netYield(card, { OFFLINE_POS: 8_333_334 }).milestoneBonusPaise).toBe(200_000);
  });
  it('ignores excluded spend and unused cards, and adds the bonus to net', () => {
    expect(netYield(card, { EXCLUDED: 10_000_000, FUEL: 5_000_000 }).milestoneBonusPaise).toBe(0);
    expect(netYield(card, {}, true).milestoneBonusPaise).toBe(0);
    const y = netYield(card, { ONLINE_SHOPPING: 10_000_000 });
    expect(y.netPaise).toBe(y.annualRewardPaise + 200_000 - y.feePaise);
  });
});

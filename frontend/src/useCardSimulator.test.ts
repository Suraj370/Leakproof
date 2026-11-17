import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import type { CreditCard } from './contracts';
import { DEFAULT_SPEND, useCardSimulator, useSimStore } from './useCardSimulator';

const flat = (id: string, bps: number): CreditCard => ({
  id, name: id, annualFeePaise: 0, feeWaiverSpendPaise: 0, baseEarnBps: bps, redeemPaisePerPoint: 100,
  rules: {}, groupCapPoints: {}, milestones: [],
});
const catalog = [flat('low', 100), flat('high', 200)];

beforeEach(() => useSimStore.setState({ spend: DEFAULT_SPEND, enabled: {} }));

describe('useCardSimulator', () => {
  it('shows money left on the table when the wallet is empty', () => {
    const { result } = renderHook(() => useCardSimulator(catalog));
    expect(result.current.walletNetPaise).toBe(0);
    expect(result.current.leftOnTablePaise).toBeGreaterThan(0);
  });

  it('recalculates when the wallet changes and the gap closes with the best card', () => {
    const { result } = renderHook(() => useCardSimulator(catalog));
    act(() => result.current.toggleCard('low'));
    const gapWithLow = result.current.leftOnTablePaise;
    act(() => result.current.toggleCard('high'));
    expect(result.current.leftOnTablePaise).toBe(0);
    expect(gapWithLow).toBeGreaterThan(0);
  });

  it('recalculates when a slider moves', () => {
    const { result } = renderHook(() => useCardSimulator(catalog));
    act(() => result.current.toggleCard('low'));
    const before = result.current.walletNetPaise;
    act(() => result.current.setSpend('DINING', 5_000_000));
    expect(result.current.walletNetPaise).toBeGreaterThan(before);
  });

  it('clamps negative spend to zero', () => {
    const { result } = renderHook(() => useCardSimulator(catalog));
    act(() => result.current.setSpend('DINING', -50));
    expect(result.current.spend.DINING).toBe(0);
  });
});

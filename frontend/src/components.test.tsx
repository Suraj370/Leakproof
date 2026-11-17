import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { CardSimulator } from './CardSimulator';
import type { CreditCard, MFReport } from './contracts';
import { inr, pct } from './format';
import { drawShareCard } from './ShareCard';
import { DEFAULT_SPEND, useSimStore } from './useCardSimulator';
import { WealthLeakSummary } from './WealthLeakSummary';

const report: MFReport = {
  funds: [
    { isin: 'A', name: 'Bluechip Regular', valuePaise: 100_000_000, terBps: 160 },
    { isin: 'B', name: 'Index Direct', valuePaise: 10_000_000, terBps: 60 },
    { isin: 'C', name: 'Consumption', valuePaise: 10_000_000, terBps: 90 },
  ],
  pairs: [
    { a: 0, b: 1, overlapBps: 3250, shared: [{ stockIsin: 'X', name: 'HDFC Bank', weightABps: 900, weightBBps: 850, overlapBps: 850 }],
      action: { verdict: 'CONSOLIDATE_NOW', exitFundIndex: 0, keepFundIndex: 1,
        exitNow: { stcgTaxPaise: 0, ltcgTaxPaise: 975_000, exitLoadPaise: 0, totalPaise: 975_000, exemptionUsedPaise: 0 }, annualSavingPaise: 1_000_000 } },
    { a: 0, b: 2, overlapBps: 200, shared: [],
      action: { verdict: 'KEEP', exitFundIndex: 0, keepFundIndex: 2, exitNow: { stcgTaxPaise: 0, ltcgTaxPaise: 0, exitLoadPaise: 0, totalPaise: 0, exemptionUsedPaise: 0 }, annualSavingPaise: 0 } },
    { a: 1, b: 2, overlapBps: 100, shared: [],
      action: { verdict: 'KEEP', exitFundIndex: 1, keepFundIndex: 2, exitNow: { stcgTaxPaise: 0, ltcgTaxPaise: 0, exitLoadPaise: 0, totalPaise: 0, exemptionUsedPaise: 0 }, annualSavingPaise: 0 } },
  ],
  annualLeakPaise: 1_000_000,
  portfolioOverlapBps: 1500,
};

describe('format', () => {
  it('formats Indian rupee grouping and percentages', () => {
    expect(inr(1_000_000_000)).toBe('₹1,00,00,000');
    expect(pct(3250)).toBe('32.5%');
  });
});

describe('WealthLeakSummary', () => {
  it('frames the leak in rupees and shows the overlap score', () => {
    render(<WealthLeakSummary report={report} />);
    expect(screen.getByTestId('mf-leak')).toHaveTextContent('₹10,000');
    expect(screen.getByText(/Portfolio overlap 15.0%/)).toBeInTheDocument();
  });

  it('drills into shared stocks and the verdict when a heatmap cell is clicked', () => {
    render(<WealthLeakSummary report={report} />);
    expect(screen.queryByTestId('pair-detail')).toBeNull();
    fireEvent.click(screen.getByLabelText('Bluechip Regular vs Index Direct: 32.5% overlap'));
    const detail = screen.getByTestId('pair-detail');
    expect(detail).toHaveTextContent('HDFC Bank');
    expect(detail).toHaveTextContent('Switch now');
    expect(detail).toHaveTextContent('₹10,000/yr');
  });

  it('is symmetric: the mirrored cell opens the same pair', () => {
    render(<WealthLeakSummary report={report} />);
    fireEvent.click(screen.getByLabelText('Index Direct vs Bluechip Regular: 32.5% overlap'));
    expect(screen.getByTestId('pair-detail')).toHaveTextContent('HDFC Bank');
  });

  it('does not show a verdict badge for pairs under the 30% threshold', () => {
    render(<WealthLeakSummary report={report} />);
    fireEvent.click(screen.getByLabelText('Bluechip Regular vs Consumption: 2.0% overlap'));
    expect(screen.getByTestId('pair-detail')).not.toHaveTextContent('Keep');
  });
});

const flat = (id: string, bps: number): CreditCard => ({
  id, name: `Card ${id}`, annualFeePaise: 0, feeWaiverSpendPaise: 0, baseEarnBps: bps, redeemPaisePerPoint: 100,
  rules: {}, groupCapPoints: {},
});

describe('CardSimulator', () => {
  beforeEach(() => useSimStore.setState({ spend: DEFAULT_SPEND, enabled: {} }));

  it('shows the rupee gap and shrinks it to zero once the best card is in the wallet', () => {
    render(<CardSimulator catalog={[flat('low', 100), flat('high', 200)]} />);
    const before = screen.getByTestId('card-leak').textContent;
    expect(before).not.toBe('₹0');
    fireEvent.click(screen.getByLabelText('Card high'));
    expect(screen.getByTestId('card-leak')).toHaveTextContent('₹0');
  });

  it('recalculates instantly when a slider moves', () => {
    render(<CardSimulator catalog={[flat('low', 100)]} />);
    const before = screen.getByTestId('card-leak').textContent;
    fireEvent.change(screen.getByLabelText('Dining monthly spend'), { target: { value: '150000' } });
    expect(screen.getByTestId('card-leak').textContent).not.toBe(before);
  });
});

describe('drawShareCard', () => {
  it('renders only the two anonymised percentages', () => {
    const calls: string[] = [];
    const ctx = { fillText: (t: string) => calls.push(t), fillRect: vi.fn(), set fillStyle(_v: string) {}, set font(_v: string) {} };
    const canvas = { getContext: () => ctx } as unknown as HTMLCanvasElement;
    drawShareCard(canvas, { rewardYieldBps: 250, overlapBps: 3250 });
    expect(calls).toContain('2.5%');
    expect(calls).toContain('32.5%');
    expect(calls.join(' ')).not.toMatch(/₹|\d{4,}/);
  });
});

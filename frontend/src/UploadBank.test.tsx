import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { uploadBankCSV } from './bankClient';
import type { BankSummary } from './contracts';
import { UploadBank } from './UploadBank';
import { DEFAULT_SPEND, useSimStore } from './useCardSimulator';

const summary: BankSummary = {
  monthlySpendPaise: { DINING: 150_000, EXCLUDED: 2_000_000 },
  months: 3,
  transactions: 12,
  lowConfidence: [{ description: 'UPI/RAMESH TEA STALL', amountPaise: 60_000, category: 'OFFLINE_POS' }],
};

beforeEach(() => useSimStore.setState({ spend: DEFAULT_SPEND, enabled: {} }));
afterEach(() => vi.unstubAllGlobals());

const csv = () => new File(['Date,Description,Debit\n'], 'stmt.csv', { type: 'text/csv' });

describe('uploadBankCSV', () => {
  it('posts the file and returns the summary', async () => {
    const f = vi.fn().mockResolvedValue(new Response(JSON.stringify(summary), { status: 200 }));
    const r = await uploadBankCSV(csv(), f as unknown as typeof fetch);
    expect(r.months).toBe(3);
    const [url, init] = f.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/v1/analyze/bank');
    expect((init.body as FormData).get('file')).toBeInstanceOf(File);
  });
  it('maps 422 and network errors', async () => {
    const f422 = vi.fn().mockResolvedValue(new Response('{}', { status: 422 }));
    await expect(uploadBankCSV(csv(), f422 as unknown as typeof fetch)).rejects.toMatchObject({ status: 422 });
    const down = vi.fn().mockRejectedValue(new TypeError('offline'));
    await expect(uploadBankCSV(csv(), down as unknown as typeof fetch)).rejects.toMatchObject({ status: 0 });
  });
});

describe('UploadBank', () => {
  it('fills the simulator sliders from the statement and zeroes categories it did not see', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify(summary), { status: 200 })));
    render(<UploadBank />);
    fireEvent.change(screen.getByLabelText('Bank statement CSV'), { target: { files: [csv()] } });
    await waitFor(() => expect(screen.getByTestId('bank-summary')).toHaveTextContent('12 transactions over 3 months'));
    const spend = useSimStore.getState().spend;
    expect(spend.DINING).toBe(150_000);
    expect(spend.EXCLUDED).toBe(2_000_000);
    expect(spend.FUEL).toBe(0);
    expect(screen.getByText(/UPI\/RAMESH TEA STALL/)).toBeInTheDocument();
  });

  it('shows an error and leaves the sliders alone on failure', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 422 })));
    render(<UploadBank />);
    fireEvent.change(screen.getByLabelText('Bank statement CSV'), { target: { files: [csv()] } });
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument());
    expect(useSimStore.getState().spend).toEqual(DEFAULT_SPEND);
  });
});

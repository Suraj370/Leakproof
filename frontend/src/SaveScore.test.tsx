import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { fetchScore, saveScore, type ScoreInput } from './reportsClient';
import { SaveScore } from './SaveScore';
import { SharedScore } from './SharedScore';

const score: ScoreInput = { rewardYieldBps: 250, overlapBps: 3250, annualLeakPaise: 1_500_000, cardLeakPaise: 500_000, mfLeakPaise: 1_000_000 };

afterEach(() => vi.unstubAllGlobals());

describe('reportsClient', () => {
  it('posts exactly the five numbers', async () => {
    const f = vi.fn().mockResolvedValue(new Response(JSON.stringify({ slug: 'abcdefghij' }), { status: 201 }));
    expect(await saveScore(score, f as unknown as typeof fetch)).toBe('abcdefghij');
    const [, init] = f.mock.calls[0] as [string, RequestInit];
    expect(Object.keys(JSON.parse(init.body as string)).sort()).toEqual(
      ['annualLeakPaise', 'cardLeakPaise', 'mfLeakPaise', 'overlapBps', 'rewardYieldBps'],
    );
  });
  it('explains rate limiting and generic failure', async () => {
    const f429 = vi.fn().mockResolvedValue(new Response('{}', { status: 429 }));
    await expect(saveScore(score, f429 as unknown as typeof fetch)).rejects.toThrow(/minute/);
    const f500 = vi.fn().mockResolvedValue(new Response('{}', { status: 500 }));
    await expect(saveScore(score, f500 as unknown as typeof fetch)).rejects.toThrow(/Could not save/);
  });
  it('never calls the server for a malformed slug', async () => {
    const f = vi.fn();
    expect(await fetchScore('../etc/passwd', f as unknown as typeof fetch)).toBeNull();
    expect(f).not.toHaveBeenCalled();
  });
  it('returns null on 404', async () => {
    const f = vi.fn().mockResolvedValue(new Response('{}', { status: 404 }));
    expect(await fetchScore('abcdefghij', f as unknown as typeof fetch)).toBeNull();
  });
});

describe('SaveScore', () => {
  it('shows the share link after saving', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ slug: 'abcdefghij' }), { status: 201 })));
    render(<SaveScore score={score} origin="https://leakproof.test" />);
    fireEvent.click(screen.getByRole('button', { name: /save and get/i }));
    await waitFor(() => expect(screen.getByTestId('share-link')).toHaveTextContent('https://leakproof.test/r/abcdefghij'));
  });
  it('shows an error when saving fails', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 500 })));
    render(<SaveScore score={score} />);
    fireEvent.click(screen.getByRole('button', { name: /save and get/i }));
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Could not save'));
  });
});

describe('SharedScore', () => {
  it('renders the saved numbers', async () => {
    const saved = { ...score, mfRulesetMonth: '2025-06', cardRulesetVersion: 'catalog-2025-06', expiresAt: '2025-09-01T00:00:00Z' };
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify(saved), { status: 200 })));
    render(<SharedScore slug="abcdefghij" />);
    await waitFor(() => expect(screen.getByTestId('shared-leak')).toHaveTextContent('₹15,000'));
    expect(screen.getByText(/Portfolio overlap 32.5%/)).toBeInTheDocument();
  });
  it('says not found for an unknown or expired score', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 404 })));
    render(<SharedScore slug="abcdefghij" />);
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('not found'));
  });
});

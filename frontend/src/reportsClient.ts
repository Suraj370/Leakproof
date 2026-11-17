import type { Paise } from './contracts';

export interface ScoreInput {
  rewardYieldBps: number;
  overlapBps: number;
  annualLeakPaise: Paise;
  cardLeakPaise: Paise;
  mfLeakPaise: Paise;
}

export interface SavedScore extends ScoreInput {
  mfRulesetMonth: string;
  cardRulesetVersion: string;
  expiresAt: string;
}

export const SLUG_RE = /^[a-z2-7]{10}$/;

/** Saves ONLY the five numbers. Returns the share slug. */
export async function saveScore(s: ScoreInput, doFetch: typeof fetch = fetch): Promise<string> {
  const resp = await doFetch('/api/v1/reports', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(s),
  });
  if (resp.status === 429) throw new Error('Too many saves just now. Try again in a minute.');
  if (!resp.ok) throw new Error('Could not save your score.');
  return ((await resp.json()) as { slug: string }).slug;
}

export async function fetchScore(slug: string, doFetch: typeof fetch = fetch): Promise<SavedScore | null> {
  if (!SLUG_RE.test(slug)) return null;
  const resp = await doFetch(`/api/v1/reports/${slug}`);
  if (resp.status === 404) return null;
  if (!resp.ok) throw new Error('Could not load this score.');
  return (await resp.json()) as SavedScore;
}

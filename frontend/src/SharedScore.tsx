import { useEffect, useState } from 'react';
import { inr, pct } from './format';
import { fetchScore, type SavedScore } from './reportsClient';

/** Public view of a saved score at /r/:slug. Numbers only, so it is safe to share in the forum. */
export function SharedScore({ slug }: { slug: string }) {
  const [score, setScore] = useState<SavedScore | null | undefined>(undefined);

  useEffect(() => {
    let alive = true;
    fetchScore(slug).then((s) => alive && setScore(s)).catch(() => alive && setScore(null));
    return () => { alive = false; };
  }, [slug]);

  if (score === undefined) return <p className="p-4 text-sm">Loading…</p>;
  if (score === null) return <p role="alert" className="p-4 text-sm">This score was not found, or it has expired.</p>;

  return (
    <main className="mx-auto max-w-md space-y-4 p-4">
      <h1 className="text-2xl font-bold">Leakproof score</h1>
      <div className="space-y-2 rounded-2xl bg-slate-900 p-6 text-white">
        <p className="text-sm text-slate-300">Annual rupees left on the table</p>
        <p className="text-4xl font-bold tabular-nums" data-testid="shared-leak">{inr(score.annualLeakPaise)}</p>
        <p className="text-sm text-slate-300">Cards {inr(score.cardLeakPaise)} · Mutual funds {inr(score.mfLeakPaise)}</p>
        <p className="pt-2 text-sm">Reward yield {pct(score.rewardYieldBps)} · Portfolio overlap {pct(score.overlapBps)}</p>
      </div>
      <a href="/" className="text-sm underline">Find your own leaks</a>
    </main>
  );
}

import { useState } from 'react';
import { saveScore, type ScoreInput } from './reportsClient';

export function SaveScore({ score, origin = window.location.origin }: { score: ScoreInput; origin?: string }) {
  const [link, setLink] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function save() {
    setBusy(true);
    setError(null);
    try {
      setLink(`${origin}/r/${await saveScore(score)}`);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-2 text-sm">
      <button
        type="button" onClick={save} disabled={busy}
        className="rounded-lg border px-4 py-2 font-medium disabled:opacity-50"
      >
        Save and get a shareable link
      </button>
      <p className="text-xs text-slate-600">Saves only these numbers (no names, funds, cards or statements) for 90 days.</p>
      {link && <p data-testid="share-link" className="break-all rounded bg-emerald-50 p-2 text-emerald-900">{link}</p>}
      {error && <p role="alert" className="rounded bg-red-50 p-2 text-red-800">{error}</p>}
    </div>
  );
}

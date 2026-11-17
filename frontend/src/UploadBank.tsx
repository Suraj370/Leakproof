import { useRef, useState } from 'react';
import { uploadBankCSV } from './bankClient';
import { CATEGORIES, type BankSummary } from './contracts';
import { inr } from './format';
import { useSimStore } from './useCardSimulator';

export function UploadBank() {
  const fileRef = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [summary, setSummary] = useState<BankSummary | null>(null);

  async function onChange() {
    const file = fileRef.current?.files?.[0];
    if (!file) return;
    setBusy(true);
    setError(null);
    try {
      const s = await uploadBankCSV(file);
      // Replace the whole profile so sliders reflect the statement exactly.
      for (const cat of CATEGORIES) useSimStore.getState().setSpend(cat, s.monthlySpendPaise[cat] ?? 0);
      setSummary(s);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      if (fileRef.current) fileRef.current.value = '';
      setBusy(false);
    }
  }

  return (
    <div className="space-y-2 rounded-2xl border p-4">
      <h2 className="font-semibold">Fill the sliders from your bank or card statement (CSV)</h2>
      <p className="text-xs text-slate-600">Processed in memory and discarded. Debits only; credits and refunds are ignored.</p>
      <input ref={fileRef} type="file" accept=".csv,text/csv" aria-label="Bank statement CSV" onChange={onChange} disabled={busy} className="block text-sm" />
      {error && <p role="alert" className="rounded bg-red-50 p-2 text-sm text-red-800">{error}</p>}
      {summary && (
        <div className="text-sm" data-testid="bank-summary">
          <p>
            Read {summary.transactions} transactions over {summary.months} month{summary.months === 1 ? '' : 's'}.
          </p>
          {summary.lowConfidence.length > 0 && (
            <details className="mt-1 text-xs text-slate-600">
              <summary>{summary.lowConfidence.length} merchants we were unsure about (counted as offline spend)</summary>
              <ul className="mt-1 space-y-0.5">
                {summary.lowConfidence.map((i, k) => (
                  <li key={k}>{i.description} — {inr(i.amountPaise)}</li>
                ))}
              </ul>
            </details>
          )}
        </div>
      )}
    </div>
  );
}

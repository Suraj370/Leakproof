import { useEffect, useState } from 'react';
import { CardSimulator } from './CardSimulator';
import type { CASReport, CreditCard, MFReport, Position } from './contracts';
import { UploadBank } from './UploadBank';
import { UploadCAS } from './UploadCAS';
import { ShareButton } from './ShareCard';
import { useCardSimulator } from './useCardSimulator';
import { WealthLeakSummary } from './WealthLeakSummary';

// Demo portfolio matching the backend seed catalog (illustrative data only).
const DEMO: Position[] = [
  { fundIsin: 'INFDEMO00001', lots: [{ purchaseDate: '2023-01-10T00:00:00Z', units: '100', costPaise: 80_000_000, valuePaise: 100_000_000 }] },
  { fundIsin: 'INFDEMO00002', lots: [{ purchaseDate: '2023-01-10T00:00:00Z', units: '100', costPaise: 9_000_000, valuePaise: 10_000_000 }] },
  { fundIsin: 'INFDEMO00003', lots: [{ purchaseDate: '2024-02-10T00:00:00Z', units: '100', costPaise: 9_000_000, valuePaise: 10_000_000 }] },
];

export function App() {
  const [catalog, setCatalog] = useState<CreditCard[]>([]);
  const [report, setReport] = useState<MFReport | null>(null);
  const [notes, setNotes] = useState<CASReport | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    fetch('/api/v1/catalog/cards')
      .then((r) => (r.ok ? (r.json() as Promise<CreditCard[]>) : Promise.reject(new Error(`catalog ${r.status}`))))
      .then(setCatalog)
      .catch((e: Error) => setError(e.message));
  }, []);

  async function runDemo() {
    setError(null);
    try {
      const r = await fetch('/api/v1/analyze/mf', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ positions: DEMO }),
      });
      if (!r.ok) throw new Error(`analyze ${r.status}`);
      setReport((await r.json()) as MFReport);
      setNotes(null);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  return (
    <main className="mx-auto max-w-3xl space-y-10 p-4">
      <h1 className="text-2xl font-bold">Leakproof</h1>
      <p className="text-sm text-slate-600">
        Guest mode: nothing you enter is stored. Fund and card data below is illustrative demo data.
      </p>
      {error && <p role="alert" className="rounded bg-red-50 p-3 text-sm text-red-800">{error}</p>}

      <UploadBank />
      <CardSimulator catalog={catalog} />

      <div className="space-y-4">
        <UploadCAS onReport={(r) => { setReport(r); setNotes(r); }} />
        <button onClick={runDemo} className="rounded-lg border px-4 py-2 text-sm font-medium">
          Or analyse a demo mutual fund portfolio
        </button>
        {notes && (notes.warnings.length > 0 || notes.unmatchedIsins.length > 0) && (
          <ul className="space-y-1 rounded bg-amber-50 p-3 text-xs text-amber-900">
            {notes.warnings.map((w) => <li key={w}>{w}</li>)}
            {notes.unmatchedIsins.length > 0 && (
              <li>Not in our catalog yet, so skipped: {notes.unmatchedIsins.join(', ')}</li>
            )}
          </ul>
        )}
        {report && <WealthLeakSummary report={report} />}
      </div>

      <Share catalog={catalog} report={report} />
    </main>
  );
}

function Share({ catalog, report }: { catalog: CreditCard[]; report: MFReport | null }) {
  const { comboYieldBps, walletYieldBps } = useCardSimulator(catalog);
  return (
    <ShareButton
      stats={{ rewardYieldBps: walletYieldBps || comboYieldBps, overlapBps: report?.portfolioOverlapBps ?? 0 }}
    />
  );
}

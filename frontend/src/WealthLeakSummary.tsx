import { useMemo, useState } from 'react';
import type { MFReport, PairResult } from './contracts';
import { inr, pct } from './format';

const VERDICT = {
  CONSOLIDATE_NOW: { label: 'Switch now', cls: 'bg-emerald-100 text-emerald-800' },
  HOLD_UNTIL_LTCG_DATE: { label: 'Wait for LTCG', cls: 'bg-amber-100 text-amber-800' },
  KEEP: { label: 'Keep', cls: 'bg-slate-100 text-slate-700' },
} as const;

export function WealthLeakSummary({ report }: { report: MFReport }) {
  const [sel, setSel] = useState<PairResult | null>(null);

  const pairMap = useMemo(() => {
    const m = new Map<string, PairResult>();
    for (const p of report.pairs) {
      m.set(`${p.a}:${p.b}`, p);
      m.set(`${p.b}:${p.a}`, p);
    }
    return m;
  }, [report.pairs]);

  return (
    <section className="space-y-6">
      <header className="rounded-2xl bg-slate-900 p-6 text-white">
        <p className="text-sm text-slate-300">Annual rupees left on the table (mutual funds)</p>
        <p className="text-4xl font-bold tabular-nums" data-testid="mf-leak">{inr(report.annualLeakPaise)}</p>
        <p className="mt-1 text-sm text-slate-300">Portfolio overlap {pct(report.portfolioOverlapBps)}</p>
      </header>

      <div className="overflow-x-auto">
        <div
          role="grid"
          aria-label="Fund overlap matrix"
          className="inline-grid gap-1"
          style={{ gridTemplateColumns: `9rem repeat(${report.funds.length}, 3.5rem)` }}
        >
          <div />
          {report.funds.map((f) => (
            <div key={f.isin} className="truncate text-center text-[10px]" title={f.name}>{f.name.slice(0, 8)}</div>
          ))}
          {report.funds.map((row, i) => (
            <div key={row.isin} className="contents">
              <div className="truncate pr-2 text-xs" title={row.name}>{row.name}</div>
              {report.funds.map((col, j) => {
                if (i === j) return <div key={col.isin} className="h-12 rounded bg-slate-200" />;
                const p = pairMap.get(`${i}:${j}`);
                const o = p?.overlapBps ?? 0;
                return (
                  <button
                    key={col.isin}
                    role="gridcell"
                    disabled={!p}
                    onClick={() => p && setSel(p)}
                    aria-label={`${row.name} vs ${col.name}: ${pct(o)} overlap`}
                    className={`h-12 rounded text-[11px] font-medium tabular-nums ${sel === p ? 'ring-2 ring-slate-900' : ''}`}
                    style={{ backgroundColor: `rgba(225, 29, 72, ${Math.min(1, o / 6000)})`, color: o > 3500 ? '#fff' : '#111' }}
                  >
                    {pct(o)}
                  </button>
                );
              })}
            </div>
          ))}
        </div>
      </div>

      {sel && <PairDetail report={report} pair={sel} />}
    </section>
  );
}

function PairDetail({ report, pair }: { report: MFReport; pair: PairResult }) {
  const a = report.funds[pair.a];
  const b = report.funds[pair.b];
  const exit = report.funds[pair.action.exitFundIndex];
  const v = VERDICT[pair.action.verdict];
  const flagged = pair.overlapBps > 3000;
  return (
    <div className="space-y-3 rounded-2xl border p-4" data-testid="pair-detail">
      <h3 className="font-semibold">{a?.name} × {b?.name}: {pct(pair.overlapBps)} overlap</h3>
      {flagged && (
        <p className="text-sm">
          <span className={`mr-2 rounded px-2 py-0.5 text-xs font-semibold ${v.cls}`}>{v.label}</span>
          {pair.action.verdict !== 'KEEP' && exit ? (
            <>
              Moving out of {exit.name} recovers <b>{inr(pair.action.annualSavingPaise)}/yr</b> in fees; one-time cost{' '}
              {inr(pair.action.exitNow.totalPaise)}
              {pair.action.holdUntil && <> — wait until {new Date(pair.action.holdUntil).toLocaleDateString('en-IN')}</>}.
            </>
          ) : (
            <>Exit cost outweighs the fee saving right now.</>
          )}
        </p>
      )}
      <ul className="divide-y text-sm">
        {pair.shared.map((s) => (
          <li key={s.stockIsin} className="flex justify-between py-1.5">
            <span>{s.name}</span>
            <span className="tabular-nums text-slate-600">{pct(s.weightABps)} / {pct(s.weightBBps)}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

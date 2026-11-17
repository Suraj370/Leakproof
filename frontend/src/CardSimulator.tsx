import { CATEGORIES, CATEGORY_LABEL, type CreditCard } from './contracts';
import { inr, pct } from './format';
import { useCardSimulator } from './useCardSimulator';

export function CardSimulator({ catalog }: { catalog: CreditCard[] }) {
  const sim = useCardSimulator(catalog);

  return (
    <section className="space-y-6">
      <header className="rounded-2xl bg-slate-900 p-6 text-white">
        <p className="text-sm text-slate-300">Annual rupees left on the table (credit cards)</p>
        <p className="text-4xl font-bold tabular-nums" data-testid="card-leak">{inr(sim.leftOnTablePaise)}</p>
        <p className="mt-1 text-sm text-slate-300">
          Your reward yield {pct(sim.walletYieldBps)} → best combo {pct(sim.comboYieldBps)}
          {sim.combo.cards.length > 0 && <> ({sim.combo.cards.map((c) => c.name).join(' + ')})</>}
        </p>
      </header>

      <fieldset className="space-y-2">
        <legend className="mb-1 font-semibold">Cards you hold</legend>
        {catalog.map((c) => (
          <label key={c.id} className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={!!sim.enabled[c.id]} onChange={() => sim.toggleCard(c.id)} />
            {c.name}
          </label>
        ))}
      </fieldset>

      <div className="space-y-3">
        <h3 className="font-semibold">Monthly spend</h3>
        {CATEGORIES.map((cat) => {
          const rupees = Math.round((sim.spend[cat] ?? 0) / 100);
          return (
            <label key={cat} className="grid grid-cols-[10rem_1fr_6rem] items-center gap-3 text-sm">
              <span>{CATEGORY_LABEL[cat]}</span>
              <input
                type="range" min={0} max={200_000} step={500} value={rupees}
                aria-label={`${CATEGORY_LABEL[cat]} monthly spend`}
                onChange={(e) => sim.setSpend(cat, Number(e.target.value) * 100)}
              />
              <span className="text-right tabular-nums">{inr(rupees * 100)}</span>
            </label>
          );
        })}
      </div>
    </section>
  );
}

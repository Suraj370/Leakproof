import { useMemo } from 'react';
import { create } from 'zustand';
import type { Category, CreditCard, Paise, Spend } from './contracts';
import { bestCombo, evaluate, yieldBps } from './engine';

interface SimState {
  spend: Spend;
  enabled: Record<string, boolean>; // the user's current wallet
  setSpend: (cat: Category, paise: Paise) => void;
  toggleCard: (id: string) => void;
}

export const DEFAULT_SPEND: Spend = {
  DINING: 800_000, GROCERIES: 1_000_000, ONLINE_SHOPPING: 1_500_000, UTILITIES: 500_000,
  FUEL: 400_000, FLIGHTS_HOTELS: 600_000, OFFLINE_POS: 500_000, EXCLUDED: 2_500_000,
};

export const useSimStore = create<SimState>()((set) => ({
  spend: DEFAULT_SPEND,
  enabled: {},
  setSpend: (cat, paise) => set((s) => ({ spend: { ...s.spend, [cat]: Math.max(0, paise) } })),
  toggleCard: (id) => set((s) => ({ enabled: { ...s.enabled, [id]: !s.enabled[id] } })),
}));

/** Real-time slider recalculation: everything below is pure client-side math, no network. */
export function useCardSimulator(catalog: CreditCard[]) {
  const spend = useSimStore((s) => s.spend);
  const enabled = useSimStore((s) => s.enabled);
  const setSpend = useSimStore((s) => s.setSpend);
  const toggleCard = useSimStore((s) => s.toggleCard);

  const derived = useMemo(() => {
    const wallet = catalog.filter((c) => enabled[c.id]);
    const current = evaluate(wallet, spend, true);
    const combo = bestCombo(catalog, wallet, spend);
    return {
      wallet,
      walletYields: current.yields,
      walletNetPaise: current.netPaise,
      walletYieldBps: yieldBps(current.netPaise, spend),
      combo,
      comboYieldBps: yieldBps(combo.netPaise, spend),
      leftOnTablePaise: Math.max(0, combo.gainPaise),
    };
  }, [catalog, enabled, spend]);

  return { spend, enabled, setSpend, toggleCard, ...derived };
}

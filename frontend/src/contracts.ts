// Mirrors the Go structs in backend/internal/{mf,cards,api}. Keep in sync (or generate with tygo).
export type Paise = number; // integer paise; safe up to ~Rs 9 x 10^13
export type Bps = number;

export type Category =
  | 'DINING' | 'GROCERIES' | 'FLIGHTS_HOTELS' | 'UTILITIES'
  | 'FUEL' | 'ONLINE_SHOPPING' | 'OFFLINE_POS' | 'EXCLUDED';

export const CATEGORIES: readonly Category[] = [
  'DINING', 'GROCERIES', 'FLIGHTS_HOTELS', 'UTILITIES', 'FUEL', 'ONLINE_SHOPPING', 'OFFLINE_POS', 'EXCLUDED',
];

export const CATEGORY_LABEL: Record<Category, string> = {
  DINING: 'Dining', GROCERIES: 'Groceries', FLIGHTS_HOTELS: 'Flights & hotels', UTILITIES: 'Utilities',
  FUEL: 'Fuel', ONLINE_SHOPPING: 'Online shopping', OFFLINE_POS: 'Offline / POS', EXCLUDED: 'Rent, insurance, govt',
};

export type Spend = Partial<Record<Category, Paise>>;

export interface CategoryRule { earnBps: Bps; excluded: boolean; capGroup: string }

export interface CreditCard {
  id: string;
  name: string;
  annualFeePaise: Paise;
  feeWaiverSpendPaise: Paise;
  baseEarnBps: Bps;
  redeemPaisePerPoint: number;
  rules: Partial<Record<Category, CategoryRule>>;
  groupCapPoints: Record<string, number>;
}

export interface CardYield {
  cardId: string;
  annualRewardPaise: Paise;
  lostToCapsPaise: Paise;
  feePaise: Paise;
  netPaise: Paise;
}

export type Verdict = 'CONSOLIDATE_NOW' | 'HOLD_UNTIL_LTCG_DATE' | 'KEEP';

export interface ExitCost {
  stcgTaxPaise: Paise;
  ltcgTaxPaise: Paise;
  exitLoadPaise: Paise;
  totalPaise: Paise;
  exemptionUsedPaise: Paise; // part of the shared Rs1.25L LTCG exemption this exit consumes
}

export interface PairAction {
  verdict: Verdict;
  exitFundIndex: number;
  keepFundIndex: number;
  exitNow: ExitCost;
  annualSavingPaise: Paise;
  holdUntil?: string;
  notes?: string[];
}

export interface SharedHolding {
  stockIsin: string; name: string; weightABps: Bps; weightBBps: Bps; overlapBps: Bps;
}

export interface PairResult { a: number; b: number; overlapBps: Bps; shared: SharedHolding[]; action: PairAction }

export interface FundSummary { isin: string; name: string; valuePaise: Paise; terBps: Bps }

export interface MFReport {
  funds: FundSummary[];
  pairs: PairResult[];
  annualLeakPaise: Paise;
  portfolioOverlapBps: Bps;
}

export interface Lot { purchaseDate: string; units: string; costPaise: Paise; valuePaise: Paise }
export interface Position { fundIsin: string; folioRef?: string; lots: Lot[] }

export interface BankItem { description: string; amountPaise: Paise; category: Category }
export interface BankSummary {
  monthlySpendPaise: Spend;
  months: number;
  transactions: number;
  lowConfidence: BankItem[];
}

export interface CASReport extends MFReport {
  source: string;
  warnings: string[];
  unmatchedIsins: string[];
}

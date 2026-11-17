const inrFmt = new Intl.NumberFormat('en-IN', { style: 'currency', currency: 'INR', maximumFractionDigits: 0 });

/** Paise -> "₹1,00,000". */
export const inr = (paise: number) => inrFmt.format(Math.round(paise / 100));

/** Basis points -> "32.5%". */
export const pct = (bps: number) => `${(bps / 100).toFixed(1)}%`;

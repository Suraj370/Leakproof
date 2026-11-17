import type { BankSummary } from './contracts';

export class BankError extends Error {
  readonly status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

/** Uploads a bank / credit-card CSV and returns the average monthly spend by category. */
export async function uploadBankCSV(file: File, doFetch: typeof fetch = fetch): Promise<BankSummary> {
  const form = new FormData();
  form.append('file', file);
  let resp: Response;
  try {
    resp = await doFetch('/api/v1/analyze/bank', { method: 'POST', body: form });
  } catch {
    throw new BankError('Could not reach the server.', 0);
  }
  if (resp.status === 422) {
    throw new BankError('We could not find date, description and debit/amount columns, or there was no spending in that file.', 422);
  }
  if (!resp.ok) throw new BankError('Something went wrong reading that file.', resp.status);
  return (await resp.json()) as BankSummary;
}

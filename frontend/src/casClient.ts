import type { CASReport } from './contracts';

export class CASError extends Error {
  readonly status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

const FRIENDLY: Record<number, string> = {
  401: 'That password did not unlock the PDF. CAMS/KFintech statements use the password you set when requesting them.',
  415: 'This PDF uses an encryption type we do not support yet.',
  422: 'We could not recognise this statement layout.',
  504: 'The statement took too long to process.',
};

/** Uploads a CAS PDF. The password travels only in this request body and is never stored client-side. */
export async function uploadCAS(file: File, password: string, doFetch: typeof fetch = fetch): Promise<CASReport> {
  const form = new FormData();
  form.append('file', file);
  form.append('password', password);
  let resp: Response;
  try {
    resp = await doFetch('/api/v1/analyze/cas', { method: 'POST', body: form });
  } catch {
    throw new CASError('Could not reach the server.', 0);
  }
  if (!resp.ok) {
    throw new CASError(FRIENDLY[resp.status] ?? 'Something went wrong reading that file.', resp.status);
  }
  return (await resp.json()) as CASReport;
}

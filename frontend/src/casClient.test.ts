import { describe, expect, it, vi } from 'vitest';
import { CASError, uploadCAS } from './casClient';

const file = new File(['%PDF'], 'cas.pdf', { type: 'application/pdf' });
const mockFetch = (status: number, body: unknown = {}) =>
  vi.fn().mockResolvedValue(new Response(JSON.stringify(body), { status }));

describe('uploadCAS', () => {
  it('posts multipart form data with the file and password', async () => {
    const f = mockFetch(200, { source: 'CAMS', funds: [], pairs: [], warnings: [], unmatchedIsins: [] });
    const r = await uploadCAS(file, 'pw', f as unknown as typeof fetch);
    expect(r.source).toBe('CAMS');
    const [url, init] = f.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/v1/analyze/cas');
    const form = init.body as FormData;
    expect(form.get('password')).toBe('pw');
    expect((form.get('file') as File).name).toBe('cas.pdf');
  });

  it('maps wrong password to a friendly message', async () => {
    await expect(uploadCAS(file, 'x', mockFetch(401) as unknown as typeof fetch)).rejects.toMatchObject({
      status: 401,
      message: expect.stringContaining('password'),
    });
  });

  it.each([415, 422, 504])('maps %i to a CASError', async (code) => {
    await expect(uploadCAS(file, 'x', mockFetch(code) as unknown as typeof fetch)).rejects.toBeInstanceOf(CASError);
  });

  it('handles network failure', async () => {
    const f = vi.fn().mockRejectedValue(new TypeError('offline'));
    await expect(uploadCAS(file, 'x', f as unknown as typeof fetch)).rejects.toMatchObject({ status: 0 });
  });
});

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { CASReport } from './contracts';
import { UploadCAS } from './UploadCAS';

const report: CASReport = {
  source: 'CAMS', funds: [], pairs: [], annualLeakPaise: 0, portfolioOverlapBps: 0, warnings: [], unmatchedIsins: [],
};

afterEach(() => vi.unstubAllGlobals());

function pick(file: File) {
  fireEvent.change(screen.getByLabelText('CAS PDF'), { target: { files: [file] } });
}

describe('UploadCAS', () => {
  it('asks for a file before submitting', () => {
    render(<UploadCAS onReport={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: /find my leaks/i }));
    expect(screen.getByRole('alert')).toHaveTextContent('Choose your CAS PDF');
  });

  it('uploads, reports the result and clears the password field', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify(report), { status: 200 })));
    const onReport = vi.fn();
    render(<UploadCAS onReport={onReport} />);
    pick(new File(['%PDF'], 'cas.pdf', { type: 'application/pdf' }));
    const pw = screen.getByLabelText('PDF password') as HTMLInputElement;
    fireEvent.change(pw, { target: { value: 'secret' } });
    fireEvent.click(screen.getByRole('button', { name: /find my leaks/i }));
    await waitFor(() => expect(onReport).toHaveBeenCalledWith(report));
    expect(pw.value).toBe('');
  });

  it('shows a friendly error for a wrong password and still clears it', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 401 })));
    render(<UploadCAS onReport={vi.fn()} />);
    pick(new File(['%PDF'], 'cas.pdf'));
    const pw = screen.getByLabelText('PDF password') as HTMLInputElement;
    fireEvent.change(pw, { target: { value: 'wrong' } });
    fireEvent.click(screen.getByRole('button', { name: /find my leaks/i }));
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('did not unlock'));
    expect(pw.value).toBe('');
  });
});

import { useRef, useState, type FormEvent } from 'react';
import { uploadCAS } from './casClient';
import type { CASReport } from './contracts';

export function UploadCAS({ onReport }: { onReport: (r: CASReport) => void }) {
  const fileRef = useRef<HTMLInputElement>(null);
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const file = fileRef.current?.files?.[0];
    if (!file) {
      setError('Choose your CAS PDF first.');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      onReport(await uploadCAS(file, password));
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setPassword(''); // never keep the password around after the request
      if (fileRef.current) fileRef.current.value = '';
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="space-y-3 rounded-2xl border p-4">
      <h2 className="font-semibold">Upload your CAMS / KFintech statement</h2>
      <p className="text-xs text-slate-600">
        Processed in memory and discarded. Nothing is saved, and your PAN, name and folio numbers are never kept.
      </p>
      <input ref={fileRef} type="file" accept="application/pdf" aria-label="CAS PDF" className="block text-sm" />
      <input
        type="password"
        autoComplete="off"
        value={password}
        onChange={(e) => setPassword(e.target.value)}
        placeholder="PDF password"
        aria-label="PDF password"
        className="block w-full rounded border p-2 text-sm"
      />
      <button disabled={busy} className="rounded-lg bg-slate-900 px-4 py-2 text-sm font-medium text-white disabled:opacity-50">
        {busy ? 'Analysing…' : 'Find my leaks'}
      </button>
      {error && <p role="alert" className="rounded bg-red-50 p-2 text-sm text-red-800">{error}</p>}
    </form>
  );
}

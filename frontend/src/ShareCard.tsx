import { pct } from './format';

export interface ShareStats { rewardYieldBps: number; overlapBps: number }

/** Draws the anonymised score card on a canvas. Only the two percentages are used: no names, no amounts. */
export function drawShareCard(canvas: HTMLCanvasElement, s: ShareStats): void {
  canvas.width = 1200;
  canvas.height = 630;
  const g = canvas.getContext('2d');
  if (!g) return;
  g.fillStyle = '#0f172a';
  g.fillRect(0, 0, 1200, 630);
  g.fillStyle = '#94a3b8';
  g.font = '32px system-ui, sans-serif';
  g.fillText('My Wealth Leak Score', 80, 120);
  g.fillStyle = '#f8fafc';
  g.font = 'bold 120px system-ui, sans-serif';
  g.fillText(pct(s.rewardYieldBps), 80, 300);
  g.fillText(pct(s.overlapBps), 80, 500);
  g.fillStyle = '#94a3b8';
  g.font = '30px system-ui, sans-serif';
  g.fillText('card reward yield', 560, 290);
  g.fillText('portfolio overlap', 560, 490);
}

export function downloadShareCard(s: ShareStats, doc: Document = document): void {
  const canvas = doc.createElement('canvas');
  drawShareCard(canvas, s);
  canvas.toBlob((blob) => {
    if (!blob) return;
    const url = URL.createObjectURL(blob);
    const a = doc.createElement('a');
    a.href = url;
    a.download = 'wealth-leak-score.png';
    a.click();
    URL.revokeObjectURL(url);
  }, 'image/png');
}

export function ShareButton({ stats }: { stats: ShareStats }) {
  return (
    <button
      type="button"
      onClick={() => downloadShareCard(stats)}
      className="rounded-lg bg-slate-900 px-4 py-2 text-sm font-medium text-white hover:bg-slate-700"
    >
      Download my score card
    </button>
  );
}

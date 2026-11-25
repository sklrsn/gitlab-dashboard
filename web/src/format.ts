export function formatDuration(sec?: number): string {
  if (sec == null || sec < 0) return "–";
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = Math.floor(sec % 60);
  if (h) return `${h}h ${m}m`;
  if (m) return `${m}m ${s}s`;
  return `${s}s`;
}

export function elapsedSince(iso?: string, now = Date.now()): number | undefined {
  if (!iso) return undefined;
  return Math.max(0, Math.round((now - Date.parse(iso)) / 1000));
}

export function timeAgo(iso?: string, now = Date.now()): string {
  const s = elapsedSince(iso, now);
  if (s == null) return "";
  if (s < 60) return "just now";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  if (d < 30) return `${d}d ago`;
  return new Date(iso!).toLocaleDateString();
}

export function fullTime(iso?: string): string {
  return iso ? new Date(iso).toLocaleString() : "";
}

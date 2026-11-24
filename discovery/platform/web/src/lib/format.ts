// Formatting helpers. Metric units follow the UCUM-style notation used by the
// API ("%", "By", "By/s", "ms", "{request}/s", "{core}", "{connection}").

const BYTES = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];

function bytes(v: number): string {
  let i = 0;
  let x = Math.abs(v);
  while (x >= 1024 && i < BYTES.length - 1) {
    x /= 1024;
    i++;
  }
  return `${(Math.sign(v) * x).toFixed(x >= 100 || i === 0 ? 0 : 1)} ${BYTES[i]}`;
}

function compact(v: number, digits = 1): string {
  const a = Math.abs(v);
  if (a >= 1e9) return `${(v / 1e9).toFixed(digits)}G`;
  if (a >= 1e6) return `${(v / 1e6).toFixed(digits)}M`;
  if (a >= 1e4) return `${(v / 1e3).toFixed(digits)}k`;
  if (a >= 100) return v.toFixed(0);
  if (a >= 1) return v.toFixed(digits);
  return v.toFixed(3);
}

/** formatValue renders a metric value with its unit for humans. */
export function formatValue(v: number, unit: string): string {
  if (!Number.isFinite(v)) return "—";
  switch (unit) {
    case "%":
      return `${v.toFixed(1)} %`;
    case "By":
      return bytes(v);
    case "By/s":
      return `${bytes(v)}/s`;
    case "ms":
      return `${compact(v)} ms`;
    case "{core}":
      return `${v.toFixed(v < 1 ? 3 : 2)} cores`;
    default: {
      const m = /^\{(\w+)\}(\/s)?$/.exec(unit);
      if (m) return `${compact(v)} ${m[1]}${v === 1 ? "" : "s"}${m[2] ?? ""}`;
      return `${compact(v)} ${unit}`;
    }
  }
}

/** relativeTime renders "3m ago" style text from an ISO timestamp. */
export function relativeTime(iso: string | undefined, now: number = Date.now()): string {
  if (!iso) return "—";
  const s = Math.round((now - new Date(iso).getTime()) / 1000);
  if (s < 0) return "just now";
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

export function shortTime(iso: string): string {
  return new Date(iso).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

export function dateTime(iso: string | undefined): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleString();
}

export function newIdempotencyKey(prefix: string): string {
  const rnd = typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : `${Date.now()}-${Math.random()}`;
  return `${prefix}-${rnd}`.slice(0, 128);
}

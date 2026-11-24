import { useId, useState } from "react";
import type { Point } from "../api/types";
import { formatValue, shortTime } from "../lib/format";

interface Props {
  points: Point[];
  unit: string;
  label: string;
  height?: number;
}

/**
 * LineChart draws avg with a min–max band as inline SVG. It is labelled for
 * assistive technology and has a "show data" table fallback, so no chart
 * library is needed for this MVP.
 */
export function LineChart({ points, unit, label, height = 120 }: Props) {
  const [table, setTable] = useState(false);
  const [hover, setHover] = useState<number | null>(null);
  const titleId = useId();
  const width = 560;
  const pad = { l: 8, r: 8, t: 10, b: 18 };
  if (points.length === 0) return <p className="muted">No samples in this range yet.</p>;

  const t0 = new Date(points[0]!.ts).getTime();
  const t1 = new Date(points[points.length - 1]!.ts).getTime();
  const lo = Math.min(...points.map((p) => p.min));
  const hi = Math.max(...points.map((p) => p.max));
  const span = hi - lo || Math.abs(hi) || 1;
  const yMin = lo - span * 0.1;
  const yMax = hi + span * 0.1;
  const x = (ts: string) => pad.l + ((new Date(ts).getTime() - t0) / Math.max(1, t1 - t0)) * (width - pad.l - pad.r);
  const y = (v: number) => pad.t + (1 - (v - yMin) / (yMax - yMin)) * (height - pad.t - pad.b);
  const line = points.map((p, i) => `${i ? "L" : "M"}${x(p.ts).toFixed(1)},${y(p.avg).toFixed(1)}`).join("");
  const band =
    points.map((p, i) => `${i ? "L" : "M"}${x(p.ts).toFixed(1)},${y(p.max).toFixed(1)}`).join("") +
    [...points]
      .reverse()
      .map((p) => `L${x(p.ts).toFixed(1)},${y(p.min).toFixed(1)}`)
      .join("") +
    "Z";
  const last = points[points.length - 1]!;
  const hoverPoint = hover !== null ? points[hover] : undefined;

  return (
    <figure className="chart">
      <svg
        viewBox={`0 0 ${width} ${height}`}
        role="img"
        aria-labelledby={titleId}
        preserveAspectRatio="none"
        onMouseLeave={() => setHover(null)}
        onMouseMove={(e) => {
          const r = e.currentTarget.getBoundingClientRect();
          const fx = ((e.clientX - r.left) / r.width) * width;
          let best = 0;
          points.forEach((p, i) => {
            if (Math.abs(x(p.ts) - fx) < Math.abs(x(points[best]!.ts) - fx)) best = i;
          });
          setHover(best);
        }}
      >
        <title id={titleId}>
          {`${label}: ${points.length} points from ${shortTime(points[0]!.ts)} to ${shortTime(last.ts)}, latest ${formatValue(last.avg, unit)}, range ${formatValue(lo, unit)} to ${formatValue(hi, unit)}`}
        </title>
        <path d={band} className="chart-band" />
        <path d={line} className="chart-line" fill="none" />
        {hoverPoint && <line x1={x(hoverPoint.ts)} x2={x(hoverPoint.ts)} y1={pad.t} y2={height - pad.b} className="chart-cursor" />}
        <text x={pad.l} y={height - 4} className="chart-axis">
          {shortTime(points[0]!.ts)}
        </text>
        <text x={width - pad.r} y={height - 4} className="chart-axis" textAnchor="end">
          {shortTime(last.ts)}
        </text>
      </svg>
      <figcaption className="chart-caption">
        <span>{hoverPoint ? `${shortTime(hoverPoint.ts)}: ${formatValue(hoverPoint.avg, unit)} (min ${formatValue(hoverPoint.min, unit)}, max ${formatValue(hoverPoint.max, unit)})` : `latest ${formatValue(last.avg, unit)}`}</span>
        <button type="button" className="btn-link" onClick={() => setTable((t) => !t)} aria-expanded={table}>
          {table ? "Hide data" : "Show data"}
        </button>
      </figcaption>
      {table && (
        <div className="table-wrap chart-table">
          <table>
            <caption className="sr-only">{label} data</caption>
            <thead>
              <tr>
                <th scope="col">Time</th>
                <th scope="col">Avg</th>
                <th scope="col">Min</th>
                <th scope="col">Max</th>
                <th scope="col">Samples</th>
              </tr>
            </thead>
            <tbody>
              {points.map((p) => (
                <tr key={p.ts}>
                  <td>{shortTime(p.ts)}</td>
                  <td>{formatValue(p.avg, unit)}</td>
                  <td>{formatValue(p.min, unit)}</td>
                  <td>{formatValue(p.max, unit)}</td>
                  <td>{p.count}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </figure>
  );
}

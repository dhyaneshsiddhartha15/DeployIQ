import { formatDate, formatMB } from "../api/stats.js";

// Okabe-Ito palette: distinguishable under the common forms of colour
// blindness (Phase 6.5). "Before" also gets a hatch pattern, so the two series
// differ in more than hue, and every bar is labelled in the table below it.
const BEFORE = "#D55E00"; // vermillion
const AFTER = "#0072B2"; // blue

const W = 720;
const H = 280;
const PAD = { top: 16, right: 16, bottom: 44, left: 64 };

function niceMax(v) {
  if (v <= 0) return 10;
  const exp = 10 ** Math.floor(Math.log10(v));
  const f = v / exp;
  const nice = f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10;
  return nice * exp;
}

// SavingsChart draws before/after estimated sizes per run, oldest to newest.
export default function SavingsChart({ runs }) {
  const data = [...runs].sort((a, b) => new Date(a.createdAt) - new Date(b.createdAt)).slice(-24);
  if (data.length === 0) return null;

  const max = niceMax(Math.max(...data.map((d) => Math.max(d.originalEstSizeMB, d.optimizedEstSizeMB))));
  const innerW = W - PAD.left - PAD.right;
  const innerH = H - PAD.top - PAD.bottom;
  const group = innerW / data.length;
  const barW = Math.min(28, (group * 0.7) / 2);
  const y = (v) => PAD.top + innerH - (v / max) * innerH;
  const ticks = [0, 0.25, 0.5, 0.75, 1].map((t) => t * max);

  const latest = data[data.length - 1];
  const summary = `Estimated image size over ${data.length} run${data.length === 1 ? "" : "s"}. Latest: ${formatMB(
    latest.originalEstSizeMB,
  )} before, ${formatMB(latest.optimizedEstSizeMB)} after.`;

  return (
    <figure className="chart">
      <div className="legend" aria-hidden="true">
        <span>
          <i className="swatch swatch-before" style={{ background: BEFORE }} /> Before (estimate)
        </span>
        <span>
          <i className="swatch" style={{ background: AFTER }} /> After (estimate)
        </span>
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={summary} preserveAspectRatio="xMidYMid meet">
        <defs>
          <pattern id="hatch" width="6" height="6" patternUnits="userSpaceOnUse" patternTransform="rotate(45)">
            <rect width="6" height="6" fill={BEFORE} />
            <line x1="0" y1="0" x2="0" y2="6" stroke="#fff" strokeOpacity="0.45" strokeWidth="2" />
          </pattern>
        </defs>
        {ticks.map((t) => (
          <g key={t}>
            <line x1={PAD.left} x2={W - PAD.right} y1={y(t)} y2={y(t)} className="grid" />
            <text x={PAD.left - 8} y={y(t)} className="axis" textAnchor="end" dominantBaseline="middle">
              {formatMB(t)}
            </text>
          </g>
        ))}
        {data.map((d, i) => {
          const cx = PAD.left + group * i + group / 2;
          return (
            <g key={d.id ?? i} data-testid="run">
              <rect
                x={cx - barW - 1}
                y={y(d.originalEstSizeMB)}
                width={barW}
                height={Math.max(0, PAD.top + innerH - y(d.originalEstSizeMB))}
                fill="url(#hatch)"
              >
                <title>{`${formatDate(d.createdAt)}: before ${formatMB(d.originalEstSizeMB)}`}</title>
              </rect>
              <rect
                x={cx + 1}
                y={y(d.optimizedEstSizeMB)}
                width={barW}
                height={Math.max(0, PAD.top + innerH - y(d.optimizedEstSizeMB))}
                fill={AFTER}
              >
                <title>{`${formatDate(d.createdAt)}: after ${formatMB(d.optimizedEstSizeMB)}`}</title>
              </rect>
              {(data.length <= 12 || i % Math.ceil(data.length / 12) === 0) && (
                <text x={cx} y={H - PAD.bottom + 18} className="axis" textAnchor="middle">
                  {new Date(d.createdAt).toLocaleDateString(undefined, { month: "short", day: "numeric" })}
                </text>
              )}
            </g>
          );
        })}
        <line x1={PAD.left} x2={W - PAD.right} y1={PAD.top + innerH} y2={PAD.top + innerH} className="baseline" />
      </svg>
      <figcaption>Sizes are estimates from the CLI, not measurements.</figcaption>
    </figure>
  );
}

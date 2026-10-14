"use client";

import React from "react";

function clamp(v: number, a: number, b: number): number {
  return Math.min(b, Math.max(a, v));
}

function chartPaths(values: number[], w: number, h: number, pad: number) {
  const n = values.length;
  const lo = Math.min(...values);
  const hi = Math.max(...values);
  const span = hi - lo || 1;
  const pts = values.map((v, i) => [
    pad + (n < 2 ? 0 : (i / (n - 1)) * (w - pad * 2)),
    h - pad - ((v - lo) / span) * (h - pad * 2),
  ] as [number, number]);
  let line = "";
  pts.forEach(([x, y], i) => {
    if (i === 0) {
      line = `M${x.toFixed(1)},${y.toFixed(1)}`;
      return;
    }
    const [px, py] = pts[i - 1];
    const cx = (px + x) / 2;
    line += ` C${cx.toFixed(1)},${py.toFixed(1)} ${cx.toFixed(1)},${y.toFixed(1)} ${x.toFixed(1)},${y.toFixed(1)}`;
  });
  const area = n
    ? `${line} L${pts[n - 1][0].toFixed(1)},${h - pad} L${pts[0][0].toFixed(1)},${h - pad} Z`
    : "";
  return { line, area, points: pts };
}

function nearestIndex(xs: number[], x: number): number {
  let best = 0;
  for (let i = 1; i < xs.length; i++) {
    if (Math.abs(xs[i] - x) < Math.abs(xs[best] - x)) {
      best = i;
    }
  }
  return best;
}

function useInView(threshold = 0.2) {
  const ref = React.useRef<HTMLElement | null>(null);
  const [inView, setInView] = React.useState(false);
  React.useEffect(() => {
    const el = ref.current;
    if (!el || inView) {
      return;
    }
    if (typeof IntersectionObserver !== "function") {
      const timer = window.setTimeout(() => setInView(true), 0);
      return () => window.clearTimeout(timer);
    }
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          setInView(true);
          io.disconnect();
        }
      },
      { threshold },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [inView, threshold]);
  return [ref, inView] as const;
}

function useReducedMotion() {
  return React.useSyncExternalStore(
    (onChange) => {
      const media = window.matchMedia("(prefers-reduced-motion: reduce)");
      media.addEventListener("change", onChange);
      return () => media.removeEventListener("change", onChange);
    },
    () => window.matchMedia("(prefers-reduced-motion: reduce)").matches,
    () => false,
  );
}

function useMounted() {
  return React.useSyncExternalStore(
    () => () => {},
    () => true,
    () => false,
  );
}

function Brackets() {
  return (
    <>
      <span className="orbit-c orbit-c-tl" aria-hidden="true" />
      <span className="orbit-c orbit-c-tr" aria-hidden="true" />
      <span className="orbit-c orbit-c-bl" aria-hidden="true" />
      <span className="orbit-c orbit-c-br" aria-hidden="true" />
    </>
  );
}

function Mark({ size = 18 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden="true">
      <circle cx="7" cy="16" r="3.2" fill="currentColor" />
      <circle cx="16" cy="8" r="3.2" fill="currentColor" />
      <circle cx="25" cy="20" r="3.2" fill="currentColor" />
      <path d="M9.5 14.5 L13.5 10 M18.5 10.5 L22.5 17" fill="none" stroke="currentColor" strokeWidth="1.6" />
    </svg>
  );
}

function FlowCard({ uid, reduced, inView }: { uid: string; reduced: boolean; inView: boolean }) {
  const mounted = useMounted();
  const [events, setEvents] = React.useState(128);
  const [side, setSide] = React.useState<"" | "left" | "right">("");

  React.useEffect(() => {
    if (reduced || !inView) {
      return;
    }
    const timer = window.setInterval(() => setEvents((n) => n + 1), 2800);
    return () => window.clearInterval(timer);
  }, [reduced, inView]);

  const workers = [
    { x: 58, y: 38, name: "demo-worker-1 · alive" },
    { x: 104, y: 22, name: "demo-worker-2 · leased" },
    { x: 150, y: 40, name: "demo-worker-3 · idle" },
    { x: 46, y: 92, name: "Next.js · demo key" },
    { x: 146, y: 114, name: "Browser · this page" },
  ];
  const L1 = "M117,67 C196,67 210,100 262,100";
  const L2 = "M117,73 C186,73 206,120 262,120";
  const R1 = "M378,100 C412,100 418,86 450,86";
  const R2 = "M378,120 C412,120 418,134 450,134";
  const rays = `${uid}rays`;
  const glow = `${uid}glow`;

  return (
    <div className="orbit-frame orbit-frame-hover orbit-wide">
      <Brackets />
      <div className="orbit-card" style={{ padding: 0, overflow: "hidden" }}>
        <svg className="orbit-diagram" viewBox="0 0 640 178" role="img" aria-label="Workers send work through the engine into Postgres history">
          <defs>
            <radialGradient id={glow} cx="320" cy="110" r="190" gradientUnits="userSpaceOnUse">
              <stop offset="0" stopColor="var(--orbit-raise)" stopOpacity="1" />
              <stop offset="1" stopColor="var(--orbit-raise)" stopOpacity="0" />
            </radialGradient>
            <linearGradient id={rays} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0" stopColor="var(--orbit-line-strong)" stopOpacity=".55" />
              <stop offset="1" stopColor="var(--orbit-line-strong)" stopOpacity="0" />
            </linearGradient>
          </defs>
          <rect x="0" y="0" width="640" height="178" fill={`url(#${glow})`} />
          {[
            "M320,110 L90.45,231.85 L121.53,251.20Z",
            "M320,110 L159.95,315.08 L195.41,323.66Z",
            "M320,110 L257.13,361.66 L294.16,359.19Z",
            "M320,110 L382.87,361.66 L345.84,359.19Z",
            "M320,110 L480.05,315.08 L444.59,323.66Z",
            "M320,110 L549.55,231.85 L518.47,251.20Z",
          ].map((d, i) => (
            <path key={i} d={d} fill={`url(#${rays})`} opacity=".5" />
          ))}

          <g onMouseEnter={() => setSide("left")} onMouseLeave={() => setSide("")}>
            <rect x="20" y="8" width="170" height="130" fill="transparent" />
            {workers.map((m, i) => (
              <line key={`l${i}`} x1={m.x} y1={m.y} x2="104" y2="70" stroke="var(--orbit-line-strong)" strokeDasharray="2 3" />
            ))}
            <g transform="translate(91,57)">
              <rect width="26" height="26" rx="5" className="orbit-node" />
              <g transform="translate(5,5)" style={{ color: "var(--orbit-ink)" }}>
                <Mark size={16} />
              </g>
            </g>
            {workers.map((m, i) => (
              <g key={i} className="orbit-av" tabIndex={0} role="img" aria-label={m.name}>
                <circle cx={m.x} cy={m.y} r="11" fill="var(--orbit-raise)" stroke="var(--orbit-line-strong)" />
                <text x={m.x} y={m.y + 3.5} textAnchor="middle" fontSize="7" fontFamily="var(--orbit-mono)" fill="var(--orbit-ink)">
                  {i < 3 ? `W${i + 1}` : i === 3 ? "NX" : "UI"}
                </text>
                <circle cx={m.x + 8} cy={m.y + 8} r="2.6" fill={i === 4 ? "var(--orbit-faint)" : "var(--orbit-ok)"} stroke="var(--orbit-paper)" className={i === 4 ? undefined : "orbit-presence"} />
                <g className="orbit-av-tip" transform={`translate(${m.x},${m.y - 18})`}>
                  <rect x={-m.name.length * 2.45 - 6} y="-9" width={m.name.length * 4.9 + 12} height="14" rx="2" fill="var(--orbit-inv)" />
                  <text x="0" y="1" textAnchor="middle" fontSize="7.5" fill="var(--orbit-inv-ink)" fontFamily="var(--orbit-sans)">
                    {m.name}
                  </text>
                </g>
              </g>
            ))}
          </g>

          {[L1, L2].map((d, i) => (
            <path key={d} d={d} className={`orbit-flow${side === "left" ? " orbit-flow-on" : ""}${i ? " orbit-dash" : ""}`} />
          ))}
          {[R1, R2].map((d, i) => (
            <path key={d} d={d} className={`orbit-flow${side === "right" ? " orbit-flow-on" : ""}${i ? " orbit-dash" : ""}`} />
          ))}
          {mounted &&
            !reduced &&
            [L1, L2, R1, R2].map((d, i) => (
              <circle key={`p${i}`} r="2.2" className="orbit-pkt">
                <animateMotion dur={`${2.2 + (i % 2) * 0.6}s`} begin={`${i * 0.35}s`} repeatCount="indefinite" path={d} />
              </circle>
            ))}

          <g transform="translate(262,88)">
            <rect x="-4" y="-4" width="124" height="52" rx="8" fill="none" stroke="var(--orbit-line)" className={reduced ? undefined : "orbit-glow"} />
            <rect width="116" height="44" rx="6" className="orbit-node" style={{ filter: "drop-shadow(0 6px 10px rgba(0,0,0,.08))" }} />
            <g transform="translate(13,14)" style={{ color: "var(--orbit-ink)" }}>
              <Mark size={16} />
            </g>
            <text x="36" y="26" fontSize="8.5" letterSpacing="1.4" fontWeight="600" fill="var(--orbit-soft)" fontFamily="var(--orbit-sans)">
              ENGINE
            </text>
            <path d="M100 18l4 4-4 4" fill="none" stroke="var(--orbit-muted)" strokeWidth="1.3" />
          </g>

          <g onMouseEnter={() => setSide("right")} onMouseLeave={() => setSide("")}>
            {[2, 1, 0].map((k) => (
              <g key={events - k} transform={`translate(${450 + k * 12},${40 - k * 10})`} opacity={1 - k * 0.25}>
                <g className={`orbit-sheet${k === 0 ? " orbit-sheet-new" : ""}`}>
                  <rect width="138" height="102" rx="3" fill="var(--orbit-raise)" stroke="var(--orbit-line-strong)" />
                  {k === 0 && (
                    <>
                      <text x="10" y="16" fontSize="7" fill="var(--orbit-muted)" fontFamily="var(--orbit-mono)">
                        {`EVENT #${events}`}
                      </text>
                      <rect x="10" y="24" width="78" height="5" rx="1" fill="var(--orbit-ink)" opacity=".75" />
                      <rect x="10" y="33" width="92" height="3" rx="1" fill="var(--orbit-line-strong)" />
                      <rect x="10" y="39" width="80" height="3" rx="1" fill="var(--orbit-line-strong)" />
                      <text x="10" y="58" fontSize="7" fill="var(--orbit-faint)" fontFamily="var(--orbit-mono)">
                        ActivityCompleted
                      </text>
                      {[18, 26, 14, 30, 22, 34].map((h, j) => (
                        <rect key={j} x={12 + j * 13} y={92 - h} width="8" height={h} fill="var(--orbit-ink)" opacity={0.25 + j * 0.12} />
                      ))}
                    </>
                  )}
                </g>
              </g>
            ))}
          </g>
        </svg>
        <div className="orbit-wide-copy" style={{ padding: "0 22px 20px" }}>
          <div>
            <h3>Workers can die</h3>
            <p>Workers lease tasks, run activities, and heartbeat. Kill one mid-run and another worker finishes the order.</p>
          </div>
          <div>
            <h3>History is the truth</h3>
            <p>Accepted commands land in Postgres in the same transaction as the follow-up work. The log keeps printing.</p>
          </div>
        </div>
      </div>
    </div>
  );
}

function ServicesCard({ reduced }: { reduced: boolean }) {
  const mounted = useMounted();
  const tools = ["Payments", "Credit", "Accounts", "Report"];
  const [hover, setHover] = React.useState(-1);
  const [auto, setAuto] = React.useState(0);

  React.useEffect(() => {
    if (reduced || hover >= 0) {
      return;
    }
    const timer = window.setInterval(() => setAuto((a) => (a + 1) % 4), 1700);
    return () => window.clearInterval(timer);
  }, [reduced, hover]);

  const on = hover >= 0 ? hover : reduced ? -1 : auto;
  const tiles = [
    { x: 66, y: 34 },
    { x: 66, y: 116 },
    { x: 234, y: 34 },
    { x: 234, y: 116 },
  ];
  const path = (i: number) => {
    const t = tiles[i];
    const dir = t.x < 150 ? -1 : 1;
    return `M${150 + dir * 17},75 H${150 + dir * 42} V${t.y} H${t.x - dir * 15}`;
  };

  return (
    <div className="orbit-frame orbit-frame-hover">
      <Brackets />
      <div className="orbit-card">
        <svg className="orbit-diagram" viewBox="0 0 300 150" role="group" aria-label="Demo services">
          {tiles.map((_, i) => (
            <path key={i} d={path(i)} className={`orbit-flow${on === i ? " orbit-flow-on" : ""}`} />
          ))}
          {mounted && !reduced && on >= 0 && (
            <circle key={on} r="2.2" className="orbit-pkt">
              <animateMotion dur="1.1s" repeatCount="indefinite" path={path(on)} />
            </circle>
          )}
          <g transform="translate(133,58)">
            <rect width="34" height="34" rx="7" className="orbit-node" style={{ filter: "drop-shadow(0 4px 8px rgba(0,0,0,.08))" }} />
            <g transform="translate(8,8)" style={{ color: "var(--orbit-ink)" }}>
              <Mark size={18} />
            </g>
          </g>
          {tiles.map((t, i) => (
            <g
              key={i}
              className={`orbit-tool${on === i ? " orbit-tool-on" : ""}`}
              tabIndex={0}
              role="img"
              aria-label={tools[i]}
              onMouseEnter={() => setHover(i)}
              onMouseLeave={() => setHover(-1)}
              onFocus={() => setHover(i)}
              onBlur={() => setHover(-1)}
            >
              <rect x={t.x - 15} y={t.y - 15} width="30" height="30" rx="6" className="orbit-node" />
              <text x={t.x} y={t.y + 3.5} textAnchor="middle" fontSize="7" fontFamily="var(--orbit-mono)" fill="var(--orbit-ink)">
                {tools[i].slice(0, 3).toUpperCase()}
              </text>
              <text
                x={t.x}
                y={t.y + (t.y < 75 ? -21 : 28)}
                textAnchor="middle"
                fontSize="8"
                fill="var(--orbit-muted)"
                fontFamily="var(--orbit-sans)"
                opacity={on === i ? 1 : 0}
                style={{ transition: "opacity .25s" }}
              >
                {tools[i]}
              </text>
            </g>
          ))}
          <text x="150" y="112" textAnchor="middle" fontSize="7" fill="var(--orbit-faint)" fontFamily="var(--orbit-mono)">
            {on >= 0 ? `calling · ${tools[on].toLowerCase()}` : "4 demo services"}
          </text>
        </svg>
        <div style={{ marginTop: "auto" }}>
          <h3>Activities hit real services</h3>
          <p>Charges, credits, accounts, and reports go over HTTP. Flip payments or credit from the live demo and watch the workflow react.</p>
        </div>
      </div>
    </div>
  );
}

function RecoveryCard({ uid, reduced }: { uid: string; reduced: boolean }) {
  const [ref, inView] = useInView(0.3);
  const [hover, setHover] = React.useState(-1);
  const W = 300;
  const H = 130;
  const vals = [9, 11, 10, 14, 12, 16, 15, 19, 18, 22, 24, 27];
  const forecastFrom = 8;
  const { line, area, points } = chartPaths(vals, W, H, 14);
  const split = clamp(forecastFrom, 1, vals.length - 1);
  const fill = `${uid}area`;
  const last = points[points.length - 1];
  const i = hover >= 0 ? hover : points.length - 1;
  const p = points[i];
  const ms = vals[i] * 100;

  const onMove = (e: React.PointerEvent<SVGSVGElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    setHover(nearestIndex(points.map((q) => q[0]), ((e.clientX - r.left) / r.width) * W));
  };

  return (
    <div className="orbit-frame orbit-frame-hover" ref={ref as React.RefObject<HTMLDivElement>} data-in={inView ? "true" : "false"}>
      <Brackets />
      <div className="orbit-card">
        <svg
          className="orbit-diagram orbit-chart"
          viewBox={`0 0 ${W} ${H + 6}`}
          onPointerMove={onMove}
          onPointerLeave={() => setHover(-1)}
          role="img"
          aria-label="Recovery time after a crash"
        >
          <defs>
            <linearGradient id={fill} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0" stopColor="var(--orbit-ink)" stopOpacity=".12" />
              <stop offset="1" stopColor="var(--orbit-ink)" stopOpacity="0" />
            </linearGradient>
          </defs>
          {[0.25, 0.5, 0.75].map((g) => (
            <line key={g} x1="14" x2={W - 14} y1={H * g} y2={H * g} stroke="var(--orbit-line)" />
          ))}
          <path d={area} fill={`url(#${fill})`} />
          <path d={line} fill="none" stroke="var(--orbit-ink)" strokeWidth="1.4" pathLength={1} className="orbit-line-draw" opacity=".85" />
          <line x1={points[split][0]} x2={points[split][0]} y1="10" y2={H - 14} stroke="var(--orbit-line-strong)" strokeDasharray="2 3" />
          <text x={points[split][0] - 4} y={H - 18} textAnchor="end" fontSize="7" fill="var(--orbit-faint)" fontFamily="var(--orbit-mono)">
            AFTER KILL
          </text>
          {hover >= 0 && <line x1={p[0]} x2={p[0]} y1="8" y2={H - 14} stroke="var(--orbit-ink)" strokeOpacity=".35" />}
          {!reduced && hover < 0 && <circle cx={last[0]} cy={last[1]} r="7" fill="var(--orbit-ink)" opacity=".15" className="orbit-glow" />}
          <circle cx={p[0]} cy={p[1]} r="3.2" fill="var(--orbit-raise)" stroke="var(--orbit-ink)" strokeWidth="1.5" />
          <g transform={`translate(${clamp(p[0], 40, W - 40)},${p[1] < 34 ? p[1] + 22 : p[1] - 12})`}>
            <rect x="-34" y="-11" width="68" height="15" rx="2" fill="var(--orbit-inv)" />
            <text x="0" y="-1" textAnchor="middle" fontSize="7.5" fill="var(--orbit-inv-ink)" fontFamily="var(--orbit-mono)">
              {`${i >= split ? "rec " : "run "}${ms}ms`}
            </text>
          </g>
        </svg>
        <div style={{ marginTop: "auto" }}>
          <h3>Recovery is bounded</h3>
          <p>A dead worker loses its lease. Another continues. Time to resume is lease length plus the poll interval.</p>
        </div>
      </div>
    </div>
  );
}

export function HowOrbit() {
  const uid = `df${React.useId().replace(/[^a-zA-Z0-9]/g, "")}`;
  const reduced = useReducedMotion();
  const [featRef, featIn] = useInView(0.12);

  return (
    <div className="orbit-root">
      <div className="orbit-shell">
        <section className="orbit-sec" aria-labelledby={`${uid}feat`}>
          <div className="orbit-reveal" ref={featRef as React.RefObject<HTMLDivElement>} data-in={featIn ? "true" : "false"}>
            <div className="orbit-head" id={`${uid}feat`}>
              <span className="orbit-tag">How it works</span>
              <h1 className="orbit-h2">
                <span>
                  The work is the <span className="orbit-muted">log</span>.
                </span>
                <span>
                  The worker is <em>replaceable</em>.
                </span>
              </h1>
              <p className="orbit-lead">One Postgres log. Workers can die. The workflow does not.</p>
            </div>
            <div className="orbit-bento">
              <FlowCard uid={uid} reduced={reduced} inView={featIn} />
              <ServicesCard reduced={reduced} />
              <RecoveryCard uid={uid} reduced={reduced} />
            </div>
          </div>
        </section>
      </div>
    </div>
  );
}

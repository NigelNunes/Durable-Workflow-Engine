"use client";

export function Stamp({
  kicker,
  text,
  sub,
  tone = "go",
}: {
  kicker: string;
  text: string;
  sub?: string;
  tone?: "go" | "kill";
}) {
  return (
    <div className={`stamp ${tone}`} role="status">
      <span className="stamp-rail" aria-hidden="true" />
      <span className="stamp-dot" aria-hidden="true" />
      <div className="stamp-body">
        <span className="stamp-kicker">{kicker}</span>
        <strong className="stamp-text">{text}</strong>
        {sub ? <p className="stamp-sub">{sub}</p> : null}
      </div>
    </div>
  );
}

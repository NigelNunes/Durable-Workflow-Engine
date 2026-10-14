export default function HowItWorksPage() {
  return (
    <main className="prose">
      <h2>Architecture</h2>
      <p>The browser talks only to this Next.js app. The app attaches the demo key, and the admin token on a kill, then calls the engine.</p>
      <svg className="diagram" viewBox="0 0 640 160" role="img" aria-label="Browser to Next.js to the engine, workers, and Postgres">
        <rect x="8" y="48" width="100" height="48" rx="8" fill="none" stroke="currentColor" />
        <text x="58" y="76" textAnchor="middle">Browser</text>
        <rect x="150" y="48" width="110" height="48" rx="8" fill="none" stroke="currentColor" />
        <text x="205" y="76" textAnchor="middle">Next.js</text>
        <rect x="302" y="48" width="100" height="48" rx="8" fill="none" stroke="currentColor" />
        <text x="352" y="76" textAnchor="middle">Engine</text>
        <rect x="444" y="16" width="110" height="40" rx="8" fill="none" stroke="currentColor" />
        <text x="499" y="40" textAnchor="middle">Workers</text>
        <rect x="444" y="96" width="110" height="40" rx="8" fill="none" stroke="currentColor" />
        <text x="499" y="120" textAnchor="middle">Postgres</text>
        <path d="M108 72 H150 M260 72 H302 M402 60 H444 M402 84 H444" fill="none" stroke="currentColor" />
      </svg>
      <h2>Crash and resume</h2>
      <p>Kill stores a crash on one living worker. Its next heartbeat returns that action once. The process exits, Compose starts the container again, and another worker finishes the order.</p>
      <svg className="diagram" viewBox="0 0 640 90" role="img" aria-label="Kill, heartbeat, exit, Compose restart, another worker finishes">
        <text x="16" y="36">Kill</text>
        <text x="110" y="36">heartbeat</text>
        <text x="250" y="36">exit 137</text>
        <text x="370" y="36">restart</text>
        <text x="490" y="36">other worker</text>
        <path d="M70 28 H100 M210 28 H240 M340 28 H360 M460 28 H480" fill="none" stroke="currentColor" />
      </svg>
      <h2>What it guarantees</h2>
      <ul>
        <li>Accepted commands are stored in Postgres in the same transaction as the follow-up work. A worker or engine crash does not drop that progress.</li>
        <li>Each activity result is recorded at most once. History never has two ActivityCompleted events for the same command.</li>
        <li>A dead worker loses its lease. Another worker continues. Recovery is bounded by the lease time plus the poll interval.</li>
        <li>A late completion from a worker that lost its lease is rejected.</li>
        <li>Failed activities retry with backoff, up to the configured limit.</li>
        <li>Timers still fire if the engine was down when they came due.</li>
        <li>Workflow code that changes its commands is rejected as non-deterministic.</li>
      </ul>
      <h2>What it does not guarantee</h2>
      <ul>
        <li>Activities run at least once. A crash after the side effect and before the result is recorded runs the activity again. Duplicate side effects stay gone only when the activity sends the same idempotency key.</li>
        <li>One Postgres database and one engine process. This is not a highly available cluster.</li>
        <li>Not production grade. The demo uses a shared key, one tenant, and no workflow versioning.</li>
      </ul>
      <p>
        <a href="https://github.com/nigelnunes/durable-flow">GitHub</a>
      </p>
    </main>
  );
}

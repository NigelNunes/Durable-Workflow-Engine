import { HowOrbit } from "@/components/how-orbit";
import { Reveal } from "@/components/reveal";

const guarantees = [
  ["Same transaction", "Accepted commands are stored in Postgres in the same transaction as the follow-up work. A worker or engine crash does not drop that progress."],
  ["Recorded once", "Each activity result is recorded at most once. History never has two ActivityCompleted events for the same command."],
  ["Lease moves on", "A dead worker loses its lease. Another worker continues. Recovery is bounded by the lease time plus the poll interval."],
  ["Late work is rejected", "A late completion from a worker that lost its lease is rejected."],
  ["Retries with backoff", "Failed activities retry with backoff, up to the configured limit."],
  ["Timers still fire", "Timers still fire if the engine was down when they came due."],
  ["Commands stay deterministic", "Workflow code that changes its commands is rejected as non-deterministic."],
];

const limits = [
  ["At least once", "Activities run at least once. A crash after the side effect and before the result is recorded runs the activity again. Duplicate side effects stay gone only when the activity sends the same idempotency key."],
  ["One database", "One Postgres database and one engine process. This is not a highly available cluster."],
  ["A demo", "Not production grade. The demo uses a shared key, one tenant, and no workflow versioning."],
];

export default function HowItWorksPage() {
  return (
    <main className="explain">
      <HowOrbit />

      <Reveal>
        <section className="explain-after">
          <p className="kicker">Guarantees</p>
          <h2>What survives a crash.</h2>
          <div className="promises">
            {guarantees.map(([title, body], index) => (
              <article key={title} className={index === guarantees.length - 1 ? "wide" : undefined}>
                <span>{String(index + 1).padStart(2, "0")}</span>
                <strong>{title}</strong>
                <p>{body}</p>
              </article>
            ))}
          </div>
        </section>
      </Reveal>

      <Reveal>
        <section className="limits explain-after">
          <p className="kicker">Limits</p>
          <h2>What this demo does not promise.</h2>
          <div className="promises">
            {limits.map(([title, body], index) => (
              <article key={title}>
                <span>{String(index + 1).padStart(2, "0")}</span>
                <strong>{title}</strong>
                <p>{body}</p>
              </article>
            ))}
          </div>
        </section>
      </Reveal>

      <Reveal>
        <p className="repo-row">
          <a className="repo" href="https://github.com/nigelnunes/durable-flow">
            Source on GitHub
          </a>
        </p>
      </Reveal>
    </main>
  );
}

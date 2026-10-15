export function messageFor(code?: string): string {
  switch (code) {
    case "cooldown":
      return "Cooldown: try again in a few seconds";
    case "no_alive_worker":
    case "last_worker":
      return "Keep at least one worker alive";
    case "at_capacity":
      return "All three worker slots are already running.";
    case "docker_unavailable":
    case "docker_failed":
      return "Docker is not available. Start workers with docker compose --profile demo up.";
    case "rate_limited":
      return "Too many starts. Wait a moment.";
    case "too_many_workflows":
      return "Too many workflows are already running.";
    case "unavailable":
      return "The demo engine is not running.";
    case "payments":
      return "Could not toggle payments.";
    case "credit":
      return "Could not toggle credit.";
    default:
      return "Something went wrong.";
  }
}

export { typeLabel } from "@/lib/scenarios";

export function formatRecovery(ms: number): string {
  if (ms >= 1000) {
    return `${(ms / 1000).toFixed(1)} s`;
  }
  return `${ms} ms`;
}

export function relativeTime(iso: string, now = Date.now()): string {
  const then = Date.parse(iso);
  if (Number.isNaN(then)) {
    return iso;
  }
  const seconds = Math.max(0, Math.round((now - then) / 1000));
  if (seconds < 60) {
    return `${seconds}s ago`;
  }
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) {
    return `${minutes}m ago`;
  }
  const hours = Math.round(minutes / 60);
  return `${hours}h ago`;
}

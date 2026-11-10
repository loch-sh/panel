/**
 * Parsing of the container state / status values returned by the Docker API,
 * so they can be translated (see `useContainerLabels`).
 */

/** `State` of a container (`ContainerInfo.State`, `ContainerInspect.State.Status`). */
export const CONTAINER_STATES = [
  "created",
  "running",
  "paused",
  "restarting",
  "removing",
  "exited",
  "dead",
] as const;
export type ContainerState = (typeof CONTAINER_STATES)[number];

export function isContainerState(value: string): value is ContainerState {
  return (CONTAINER_STATES as readonly string[]).includes(value);
}

export type ContainerHealth = "healthy" | "unhealthy" | "starting";

export interface ParsedStatus {
  kind: "up" | "paused" | "exited" | "restarting" | "created" | "removing" | "dead";
  /** Time elapsed since start (up/paused) or since the event (exited/restarting). */
  elapsedMs?: number;
  exitCode?: number;
  health?: ContainerHealth;
}

const UNIT_MS = {
  second: 1_000,
  minute: 60_000,
  hour: 3_600_000,
  day: 86_400_000,
  week: 604_800_000,
  month: 2_592_000_000, // go-units: 30 days
  year: 31_536_000_000, // go-units: 365 days
} as const;

/**
 * Parses a Docker human duration (github.com/docker/go-units `HumanDuration`):
 * "Less than a second", "About a minute", "About an hour", "5 seconds", "3 hours",
 * "2 weeks", "7 months"… Returns undefined for anything else.
 */
export function parseDockerDuration(text: string): number | undefined {
  const value = text.trim().toLowerCase();
  if (value === "less than a second") return 0;
  if (value === "about a minute") return UNIT_MS.minute;
  if (value === "about an hour") return UNIT_MS.hour;

  const match = /^(\d+) (second|minute|hour|day|week|month|year)s?$/.exec(value);
  if (!match) return undefined;
  return Number(match[1]) * UNIT_MS[match[2] as keyof typeof UNIT_MS];
}

const SUFFIX = /\s*\((healthy|unhealthy|health: starting|Paused)\)$/;

/**
 * Parses a container `Status` as produced by the Docker daemon
 * ("Up 3 hours (healthy)", "Exited (137) 7 months ago", "Created", …).
 * Returns undefined when the format is not recognized, so callers can fall
 * back to the raw value.
 */
export function parseDockerStatus(status: string): ParsedStatus | undefined {
  let rest = status.trim();
  let health: ContainerHealth | undefined;
  let paused = false;

  // Suffixes may be combined: "Up 2 hours (healthy) (Paused)"
  for (let m = SUFFIX.exec(rest); m; m = SUFFIX.exec(rest)) {
    if (m[1] === "Paused") paused = true;
    else health = m[1] === "health: starting" ? "starting" : (m[1] as ContainerHealth);
    rest = rest.slice(0, m.index);
  }

  if (rest === "Created") return { kind: "created" };
  if (rest === "Removal In Progress") return { kind: "removing" };
  if (rest === "Dead") return { kind: "dead" };

  const up = /^Up (.+)$/.exec(rest);
  if (up) {
    const elapsedMs = parseDockerDuration(up[1]);
    if (elapsedMs === undefined) return undefined;
    return { kind: paused ? "paused" : "up", elapsedMs, health };
  }

  const ended = /^(Exited|Restarting) \((-?\d+)\) (.+) ago$/.exec(rest);
  if (ended) {
    const elapsedMs = parseDockerDuration(ended[3]);
    if (elapsedMs === undefined) return undefined;
    return {
      kind: ended[1] === "Exited" ? "exited" : "restarting",
      exitCode: Number(ended[2]),
      elapsedMs,
    };
  }

  return undefined;
}

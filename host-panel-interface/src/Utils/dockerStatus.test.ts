import { describe, expect, it } from "vitest";
import { parseDockerDuration, parseDockerStatus } from "./dockerStatus";

describe("parseDockerDuration", () => {
  it.each([
    ["Less than a second", 0],
    ["About a minute", 60_000],
    ["3 hours", 3 * 3_600_000],
    ["1 week", 604_800_000],
  ])("%s", (text, ms) => expect(parseDockerDuration(text)).toBe(ms));

  it("refuses an unknown format", () => {
    expect(parseDockerDuration("soon")).toBeUndefined();
  });
});

describe("parseDockerStatus", () => {
  it("reads the health and pause suffixes", () => {
    expect(parseDockerStatus("Up 2 hours (healthy) (Paused)")).toEqual({
      kind: "paused",
      elapsedMs: 2 * 3_600_000,
      health: "healthy",
    });
  });

  it("reads an exit code", () => {
    expect(parseDockerStatus("Exited (137) 5 days ago")).toMatchObject({
      kind: "exited",
      exitCode: 137,
    });
  });

  it("returns undefined for an unknown status (raw value shown)", () => {
    expect(parseDockerStatus("Up sometimes")).toBeUndefined();
  });
});

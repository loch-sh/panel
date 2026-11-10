import { describe, expect, it } from "vitest";
import { deploymentDate } from "./functions.model";

describe("deploymentDate", () => {
  it("reads the date of a deployment id", () => {
    expect(deploymentDate("20261105T120000Z-1a2b3c4d")?.toISOString()).toBe(
      "2026-11-05T12:00:00.000Z",
    );
  });

  it("refuses dates out of range instead of rolling them over", () => {
    expect(deploymentDate("20261305T120000Z-1a2b3c4d")).toBeNull();
    expect(deploymentDate("20260231T120000Z-1a2b3c4d")).toBeNull();
    expect(deploymentDate("20261100T120000Z-1a2b3c4d")).toBeNull();
    expect(deploymentDate("20261105T250000Z-1a2b3c4d")).toBeNull();
    expect(deploymentDate("not-an-id")).toBeNull();
    expect(deploymentDate(undefined)).toBeNull();
  });
});

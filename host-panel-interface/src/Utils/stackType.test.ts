import { describe, expect, it } from "vitest";
import { filterStackType, isStackTypeFilter } from "./stackType";

const groups = [
  { key: "hosted-blog", hosted: true, serviceEngine: null },
  { key: "svc-nats-events", hosted: false, serviceEngine: "nats" as const },
  { key: "traefik", hosted: false, serviceEngine: null },
];

const keys = (type: Parameters<typeof filterStackType>[1]) =>
  filterStackType(groups, type).map((g) => g.key);

describe("filterStackType", () => {
  it("keeps the stacks of a kind", () => {
    expect(keys("all")).toEqual(["hosted-blog", "svc-nats-events", "traefik"]);
    expect(keys("hosting")).toEqual(["hosted-blog"]);
    expect(keys("services")).toEqual(["svc-nats-events"]);
    expect(keys("other")).toEqual(["traefik"]);
  });

  it("validates the query parameter", () => {
    expect(isStackTypeFilter("services")).toBe(true);
    expect(isStackTypeFilter("hosted")).toBe(false);
    expect(isStackTypeFilter(null)).toBe(false);
  });
});

import { describe, expect, it } from "vitest";
import { serviceEngineOf } from "./serviceStack";

const labels = (extra: Record<string, string>) => ({
  "com.docker.compose.project": "svc-nats-events",
  "sh.loch.instance": "nats-events",
  "sh.loch.service": "nats",
  ...extra,
});

describe("serviceEngineOf", () => {
  it("recognizes a dedicated or shared service instance", () => {
    expect(serviceEngineOf(labels({}))).toBe("nats");
    expect(
      serviceEngineOf(
        labels({
          "com.docker.compose.project": "svc-postgres-17",
          "sh.loch.instance": "postgres-17",
          "sh.loch.service": "postgres",
        }),
      ),
    ).toBe("postgres");
  });

  it("ignores other stacks", () => {
    expect(serviceEngineOf(null)).toBeNull();
    expect(serviceEngineOf({})).toBeNull();
    expect(
      serviceEngineOf(labels({ "com.docker.compose.project": "shop" })),
    ).toBeNull();
    expect(serviceEngineOf(labels({ "sh.loch.instance": "" }))).toBeNull();
    expect(
      serviceEngineOf(labels({ "sh.loch.service": "unknown" })),
    ).toBeNull();
  });
});

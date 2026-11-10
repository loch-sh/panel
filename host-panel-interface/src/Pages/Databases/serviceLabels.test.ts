import { describe, expect, it } from "vitest";
import { formatSize } from "./serviceLabels";

describe("formatSize", () => {
  it("shows whole gigabytes as GB", () => {
    expect(formatSize(10240, "en")).toBe("10 GB");
    // Intl separates the unit with a narrow no-break space.
    expect(formatSize(1024, "fr")).toMatch(/^1\s?Go$/u);
  });

  it("shows custom sizes in GB with decimals, small ones in MB", () => {
    expect(formatSize(256, "en")).toBe("256 MB");
    expect(formatSize(1536, "en")).toBe("1.5 GB");
    expect(formatSize(2176, "en")).toBe("2.125 GB");
    expect(formatSize(10485760, "en")).toBe("10 TB");
  });
});

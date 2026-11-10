import { describe, expect, it } from "vitest";
import { overlappingRange, subnetProblem } from "./subnet";

const reserved = ["192.168.1.0/24", "10.8.0.0/16"];

describe("subnet rules", () => {
  it("finds the reserved range a subnet overlaps, either way", () => {
    expect(overlappingRange("10.8.4.0/24", reserved)).toBe("10.8.0.0/16");
    expect(overlappingRange("192.168.0.0/16", reserved)).toBe("192.168.1.0/24");
    expect(overlappingRange("10.9.0.0/16", reserved)).toBeUndefined();
    expect(overlappingRange("nope", reserved)).toBeUndefined();
  });

  it("refuses a reserved range after the other rules", () => {
    expect(subnetProblem("10.8.4.0/24", { reservedRanges: reserved })).toEqual({
      problem: "reserved",
      range: "10.8.0.0/16",
    });
    expect(subnetProblem("8.8.0.0/16", { reservedRanges: reserved })).toEqual({
      problem: "privateRange",
    });
    expect(subnetProblem("10.20.0.0/24", { reservedRanges: reserved })).toBe(
      null,
    );
  });

  it("applies the prefix bounds of the configuration", () => {
    expect(subnetProblem("10.20.0.0/24", { maxPrefix: 20 })).toEqual({
      problem: "prefixLength",
    });
  });
});

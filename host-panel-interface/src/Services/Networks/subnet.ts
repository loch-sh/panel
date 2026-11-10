/**
 * Client-side checks of IPv4 subnets and addresses, mirroring the API rules
 * as hints (the server stays the source of truth).
 */

/** IPv4 address as an unsigned 32-bit number, null when invalid. */
export function parseIPv4(value: string): number | null {
  const parts = value.split(".");
  if (parts.length !== 4) return null;
  let n = 0;
  for (const p of parts) {
    if (!/^\d{1,3}$/.test(p)) return null;
    // No leading zero (ambiguous octal notation), like Go's netip.
    if (p.length > 1 && p.startsWith("0")) return null;
    const v = Number(p);
    if (v > 255) return null;
    n = n * 256 + v;
  }
  return n;
}

export function formatIPv4(n: number): string {
  return [24, 16, 8, 0].map((s) => Math.floor(n / 2 ** s) % 256).join(".");
}

export interface IPv4Prefix {
  address: number;
  bits: number;
}

export function parseIPv4Prefix(value: string): IPv4Prefix | null {
  const m = /^([^/]+)\/(\d{1,2})$/.exec(value.trim());
  if (!m) return null;
  const address = parseIPv4(m[1]);
  const bits = Number(m[2]);
  if (address === null || bits > 32) return null;
  return { address, bits };
}

function mask(bits: number): number {
  return bits === 0 ? 0 : (0xffffffff << (32 - bits)) >>> 0;
}

/** The network address of a prefix (address with the host bits cleared). */
export function networkAddress(p: IPv4Prefix): number {
  return (p.address & mask(p.bits)) >>> 0;
}

export function prefixContains(p: IPv4Prefix, address: number): boolean {
  return (address & mask(p.bits)) >>> 0 === networkAddress(p);
}

/** Ranges a private network subnet may come from (API rule). */
export const PRIVATE_RANGES = [
  "10.0.0.0/8",
  "172.16.0.0/12",
  "192.168.0.0/16",
  "100.64.0.0/10",
];
export const MIN_SUBNET_BITS = 16;
export const MAX_SUBNET_BITS = 28;

export type SubnetProblem =
  "format" | "networkAddress" | "prefixLength" | "privateRange" | "reserved";

/** Rules of GET /networks/config (defaults: the API's own). */
export interface SubnetRules {
  privateRanges?: readonly string[];
  reservedRanges?: readonly string[];
  minPrefix?: number;
  maxPrefix?: number;
}

/** Whether two prefixes share at least one address. */
export function prefixesOverlap(a: IPv4Prefix, b: IPv4Prefix): boolean {
  const bits = Math.min(a.bits, b.bits);
  const m = mask(bits);
  return (a.address & m) >>> 0 === (b.address & m) >>> 0;
}

/** The first range a subnet overlaps, undefined when none (or invalid). */
export function overlappingRange(
  subnet: string,
  ranges: readonly string[],
): string | undefined {
  const p = parseIPv4Prefix(subnet);
  if (!p) return undefined;
  return ranges.find((r) => {
    const range = parseIPv4Prefix(r);
    return range !== null && prefixesOverlap(p, range);
  });
}

/** Why a subnet would be refused, null when it looks valid. */
export function subnetProblem(
  value: string,
  rules: SubnetRules = {},
): {
  problem: SubnetProblem;
  expected?: string;
  range?: string;
} | null {
  const privateRanges = rules.privateRanges?.length
    ? rules.privateRanges
    : PRIVATE_RANGES;
  const minBits = rules.minPrefix ?? MIN_SUBNET_BITS;
  const maxBits = rules.maxPrefix ?? MAX_SUBNET_BITS;
  const p = parseIPv4Prefix(value);
  if (!p) return { problem: "format" };
  if (networkAddress(p) !== p.address) {
    return {
      problem: "networkAddress",
      expected: `${formatIPv4(networkAddress(p))}/${p.bits}`,
    };
  }
  if (p.bits < minBits || p.bits > maxBits) {
    return { problem: "prefixLength" };
  }
  const inPrivate = privateRanges.some((r) => {
    const range = parseIPv4Prefix(r);
    return (
      range !== null && range.bits <= p.bits && prefixContains(range, p.address)
    );
  });
  if (!inPrivate) return { problem: "privateRange" };
  const range = overlappingRange(value, rules.reservedRanges ?? []);
  return range ? { problem: "reserved", range } : null;
}

/** Number of usable addresses of a prefix (network, broadcast and gateway excluded). */
export function usableHosts(bits: number): number {
  return Math.max(0, 2 ** (32 - bits) - 3);
}

/** Loopback addresses (127.0.0.0/8 and ::1). */
export function isLoopback(address: string): boolean {
  if (address === "::1") return true;
  const n = parseIPv4(address);
  return n !== null && Math.floor(n / 2 ** 24) === 127;
}

/**
 * Whether an address is inside one of the allowed CIDRs (IPv4 only: null for
 * an IPv6 address, left to the server).
 */
export function addressAllowed(
  address: string,
  allowedCidrs: readonly string[],
): boolean | null {
  const n = parseIPv4(address);
  if (n === null) return address.includes(":") ? null : false;
  return allowedCidrs.some((c) => {
    const p = parseIPv4Prefix(c);
    return p !== null && prefixContains(p, n);
  });
}

/** The single addresses of the allowed CIDRs (/32 entries), to suggest. */
export function singleAddresses(allowedCidrs: readonly string[]): string[] {
  return allowedCidrs.flatMap((c) => {
    const [addr, bits] = c.split("/");
    if (bits === "32" && parseIPv4(addr) !== null) return [addr];
    if (bits === "128") return [addr];
    return [];
  });
}

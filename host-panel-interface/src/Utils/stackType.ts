import type { ServiceEngineId } from "../Services/Databases/Models/databases.model";

/** Kind of stack shown: every stack, hosted sites, service instances, the rest. */
export type StackTypeFilter = "all" | "hosting" | "services" | "other";

export const STACK_TYPE_FILTERS: StackTypeFilter[] = [
  "all",
  "hosting",
  "services",
  "other",
];

export function isStackTypeFilter(value: unknown): value is StackTypeFilter {
  return STACK_TYPE_FILTERS.includes(value as StackTypeFilter);
}

/** Keeps the stacks of a kind. */
export function filterStackType<
  T extends { hosted: boolean; serviceEngine: ServiceEngineId | null },
>(groups: T[], type: StackTypeFilter): T[] {
  switch (type) {
    case "hosting":
      return groups.filter((g) => g.hosted);
    case "services":
      return groups.filter((g) => g.serviceEngine !== null);
    case "other":
      return groups.filter((g) => !g.hosted && g.serviceEngine === null);
    default:
      return groups;
  }
}

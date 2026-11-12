import type { ContainerInfo } from "dockerode";
import type { ServiceEngineId } from "../../Services/Databases/Models/databases.model";
import { displayStackName, isHostedContainer } from "../../Utils/hostedStack";
import { serviceEngineOf } from "../../Utils/serviceStack";

/** Key of the group of the containers outside any Compose project. */
export const STANDALONE_KEY = "__standalone__";

export interface StackGroup {
  /** real compose project (used for the API), or STANDALONE_KEY */
  key: string;
  /** name shown to the user ("hosted-" prefix stripped) */
  stackName: string;
  /** stack managed by the hosting feature */
  hosted: boolean;
  /** engine of a stack of the services module (database, cache...) */
  serviceEngine: ServiceEngineId | null;
  containers: ContainerInfo[];
  runningCount: number;
  healthyCount: number;
  totalCount: number;
  hasErrorExits: boolean;
}

/** Groups the containers by Compose project (standalone ones first). */
export function groupByStack(
  containers: ContainerInfo[],
  noStackLabel: string,
): StackGroup[] {
  const groups = new Map<string, ContainerInfo[]>();
  for (const container of containers) {
    const stack = container.Labels["com.docker.compose.project"] ?? "";
    if (!groups.has(stack)) groups.set(stack, []);
    groups.get(stack)!.push(container);
  }

  return Array.from(groups.entries())
    .map(([stackName, items]): StackGroup => ({
      key: stackName || STANDALONE_KEY,
      stackName: stackName ? displayStackName(stackName) : noStackLabel,
      hosted: items.some((c) => isHostedContainer(c.Labels)),
      serviceEngine:
        items.map((c) => serviceEngineOf(c.Labels)).find(Boolean) ?? null,
      containers: items,
      runningCount: items.filter((c) => c.State === "running").length,
      healthyCount: items.filter(
        (c) =>
          c.State === "running" ||
          (c.State === "exited" && c.Status.includes("(0)")),
      ).length,
      totalCount: items.length,
      hasErrorExits: items.some(
        (c) => c.State === "exited" && !c.Status.includes("(0)"),
      ),
    }))
    .sort((a, b) => {
      if (a.key === STANDALONE_KEY) return -1;
      if (b.key === STANDALONE_KEY) return 1;
      return a.stackName.localeCompare(b.stackName);
    });
}

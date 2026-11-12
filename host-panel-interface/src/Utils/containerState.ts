import type { BadgeProps } from "antd";

/** Badge status (colour) of a Docker container state. */
export const CONTAINER_STATE_BADGE: Record<string, BadgeProps["status"]> = {
  running: "success",
  restarting: "processing",
  paused: "warning",
  created: "default",
  removing: "warning",
  exited: "error",
  dead: "error",
};

/** Badge status of a state ("default" for an unknown one). */
export function containerStateBadge(state: string): BadgeProps["status"] {
  return CONTAINER_STATE_BADGE[state] ?? "default";
}

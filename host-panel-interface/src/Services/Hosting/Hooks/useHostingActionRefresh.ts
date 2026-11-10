import { useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import {
  ContainerActionEnum,
  StackActionEnum,
} from "../../Containers/Models/containers.model";
import { ExpectedState, watchSettle } from "../actionSettle";
import { hostingServiceKeys } from "../hosting.service";

type Action = ContainerActionEnum | StackActionEnum;
/** Actions after which the containers settle in a known state. */
type SettleAction = Exclude<Action, ContainerActionEnum.DELETE>;

/** State the containers reach after each action. */
const EXPECTED: Record<SettleAction, ExpectedState> = {
  start: "running",
  restart: "running",
  stop: "stopped",
  kill: "stopped",
};

const isSettleAction = (action: Action): action is SettleAction =>
  action in EXPECTED;

/**
 * Refreshes the hosting stacks after a container or stack action. When a
 * start/stop/restart/kill succeeded, the stack polls until its containers reach the expected
 * state (Docker may still report the previous one right after the action).
 */
export default function useHostingActionRefresh() {
  const client = useQueryClient();

  return useCallback(
    (
      name: string,
      action: Action,
      succeeded: boolean,
      containerId?: string,
    ) => {
      if (succeeded && isSettleAction(action))
        watchSettle(name, EXPECTED[action], containerId);
      client.invalidateQueries({ queryKey: [hostingServiceKeys.stack, name] });
      client.invalidateQueries({ queryKey: [hostingServiceKeys.stacks] });
    },
    [client],
  );
}

import { useSyncExternalStore } from "react";
import { getSettleWatch, subscribeSettle } from "../actionSettle";

/** True while the status of the stack is refreshed after an action. */
export default function useIsSettling(name: string) {
  return useSyncExternalStore(subscribeSettle, () => !!getSettleWatch(name));
}

import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import { toast } from "react-toastify";
import useHostingOperationWatcher, {
  OperationFinishedHandler,
  WatchedStack,
} from "../../../Services/Hosting/Hooks/useHostingOperationWatcher";
import { hostingServiceKeys } from "../../../Services/Hosting/hosting.service";
import { invalidateAfterOperation } from "../../../Services/Hosting/invalidateAfterOperation";
import { HostingStackDetail } from "../../../Services/Hosting/Models/hosting.model";

/**
 * Reacts to the end of the operations of the stack shown in the detail page:
 * toasts, `onFailed` (e.g. open the operation tab), and after a deletion,
 * forgets the stack and goes back to the list. Returns `deleted` (true once
 * deleted) and `track`, to follow an operation started from the page.
 */
export default function useStackOperationEvents(
  name: string,
  stack: HostingStackDetail | undefined,
  /** the API answered 404 (and no request is in flight) */
  notFound: boolean,
  onFailed: () => void,
): { deleted: boolean; track: (operationId: string) => void } {
  const { t } = useTranslation("hosting");
  const navigate = useNavigate();
  const client = useQueryClient();
  const [deleted, setDeleted] = useState(false);

  // The stack seen by the watcher: none once it is gone, so a delete operation
  // seen running is reported as finished (operation = null).
  const watched = useMemo<WatchedStack[] | undefined>(() => {
    if (notFound) return [];
    return stack
      ? [{ name: stack.name, operation: stack.operation }]
      : undefined;
  }, [notFound, stack]);

  const onFinished = useCallback<OperationFinishedHandler>(
    (stackName, op) => {
      if (!op) {
        setDeleted(true);
      } else if (op.state === "succeeded") {
        toast.success(t("toasts.operationSucceeded", { name: stackName }));
      } else {
        toast.error(t("toasts.operationFailed", { name: stackName }));
        onFailed();
      }
    },
    [t, onFailed],
  );
  const trackStack = useHostingOperationWatcher(watched, onFinished);
  const track = useCallback(
    (operationId: string) => trackStack(name, operationId),
    [trackStack, name],
  );

  // The stack is gone: forget it (a new stack may reuse the name) and go
  // back to the list.
  useEffect(() => {
    if (!deleted) return;
    client.removeQueries({ queryKey: [hostingServiceKeys.stack, name] });
    invalidateAfterOperation(client);
    toast.success(t("toasts.deleted", { name }));
    navigate("/hosting", { replace: true });
  }, [client, deleted, name, navigate, t]);

  return { deleted, track };
}

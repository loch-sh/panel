import type { FormInstance } from "antd";
import { useEffect, useRef } from "react";
import { scrollToFirstError } from "./formState";
import { HostingFormValues } from "./formValues";

type Names = Parameters<FormInstance<HostingFormValues>["getFieldsError"]>[0];

/**
 * `scrollToFirstError` bound to a component: a new call replaces the pending
 * one, and unmounting cancels it.
 */
export default function useScrollToFirstError(
  form: FormInstance<HostingFormValues>,
) {
  const cancel = useRef<() => void>(undefined);
  useEffect(() => () => cancel.current?.(), []);
  return (names?: Names) => {
    cancel.current?.();
    cancel.current = scrollToFirstError(form, names);
  };
}

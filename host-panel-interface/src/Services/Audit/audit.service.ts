import { AxiosInstance } from "../../Utils/axios";
import { AuditFilters, AuditPageModel } from "./Models/audit.model";

export const auditServiceKeys = {
  entries: "audit-entries",
};

/** The audit trail is read-only: the API has no route to change it. */
export const auditService = {
  list: async (filters: AuditFilters, before?: number) => {
    const params = new URLSearchParams();
    if (filters.from) params.set("from", filters.from);
    if (filters.to) params.set("to", filters.to);
    if (filters.user) params.set("user", filters.user);
    if (filters.q) params.set("q", filters.q);
    filters.events?.forEach((e) => params.append("event", e));
    filters.excludeEvents?.forEach((e) => params.append("exclude", e));
    filters.levels?.forEach((l) => params.append("level", l));
    if (before) params.set("before", String(before));
    const { data } = await AxiosInstance.get<AuditPageModel>("/audit", {
      params,
    });
    return data;
  },
};

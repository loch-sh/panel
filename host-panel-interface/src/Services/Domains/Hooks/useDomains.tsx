import { useQuery } from "@tanstack/react-query";
import { domainsService, domainsServiceKeys } from "../domains.service";
import { secondsToMilliseconds } from "date-fns";
import { DomainEntry } from "../Models/domains.model";

export default function useDomains(enabled = true) {
  return useQuery({
    queryFn: domainsService.list,
    queryKey: [domainsServiceKeys.domains],
    enabled,
    refetchInterval: secondsToMilliseconds(5),
    structuralSharing: (oldData, newData) => {
      if (!oldData) return newData;
      if (JSON.stringify(oldData) === JSON.stringify(newData)) {
        return oldData as DomainEntry[];
      }
      return newData as DomainEntry[];
    },
  });
}

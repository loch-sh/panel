import { useQuery } from "@tanstack/react-query";
import { sftpService, sftpServiceKeys } from "../sftp.service";

/** `enabled`: false for non-admins, the endpoint would answer 403. */
export default function useSftpConfig(enabled = true) {
  return useQuery({
    enabled,
    queryFn: sftpService.config,
    queryKey: [sftpServiceKeys.config],
    staleTime: Infinity,
    // Restricted users get 403 on this admin endpoint; don't retry.
    retry: false,
  });
}

import { useQuery } from "@tanstack/react-query";
import { sftpService, sftpServiceKeys } from "../sftp.service";

export default function useSftpSites(enabled = true) {
  return useQuery({
    queryFn: sftpService.listSites,
    queryKey: [sftpServiceKeys.sites],
    enabled,
  });
}

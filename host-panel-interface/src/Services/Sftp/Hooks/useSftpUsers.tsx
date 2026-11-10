import { useQuery } from "@tanstack/react-query";
import { sftpService, sftpServiceKeys } from "../sftp.service";

export default function useSftpUsers(enabled = true) {
  return useQuery({
    queryFn: sftpService.listUsers,
    queryKey: [sftpServiceKeys.users],
    enabled,
  });
}

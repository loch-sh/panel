import { useMutation, useQueryClient } from "@tanstack/react-query";
import { sftpService, sftpServiceKeys } from "../sftp.service";

export default function useSftpUserDelete() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: sftpService.delete,
    onSettled: () => {
      client.invalidateQueries({ queryKey: [sftpServiceKeys.users] });
      client.invalidateQueries({ queryKey: [sftpServiceKeys.sites] });
    },
  });
}

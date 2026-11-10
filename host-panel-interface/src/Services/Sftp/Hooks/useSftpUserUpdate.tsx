import { useMutation, useQueryClient } from "@tanstack/react-query";
import { sftpService, sftpServiceKeys } from "../sftp.service";

export default function useSftpUserUpdate() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: sftpService.update,
    onSettled: () => {
      client.invalidateQueries({ queryKey: [sftpServiceKeys.users] });
      client.invalidateQueries({ queryKey: [sftpServiceKeys.sites] });
    },
  });
}

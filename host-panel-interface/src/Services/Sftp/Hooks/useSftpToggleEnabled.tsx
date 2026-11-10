import { useMutation, useQueryClient } from "@tanstack/react-query";
import { sftpService, sftpServiceKeys } from "../sftp.service";

export default function useSftpToggleEnabled() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: sftpService.setEnabled,
    onSettled: () => {
      client.invalidateQueries({ queryKey: [sftpServiceKeys.users] });
      client.invalidateQueries({ queryKey: [sftpServiceKeys.sites] });
    },
  });
}

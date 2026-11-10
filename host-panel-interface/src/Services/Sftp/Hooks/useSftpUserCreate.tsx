import { useMutation, useQueryClient } from "@tanstack/react-query";
import { hostingServiceKeys } from "../../Hosting/hosting.service";
import { sftpService, sftpServiceKeys } from "../sftp.service";

export default function useSftpUserCreate() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: sftpService.create,
    onSettled: () => {
      client.invalidateQueries({ queryKey: [sftpServiceKeys.users] });
      client.invalidateQueries({ queryKey: [sftpServiceKeys.sites] });
      // The user's site directory may be new: it can now host a stack.
      client.invalidateQueries({ queryKey: [hostingServiceKeys.sites] });
    },
  });
}

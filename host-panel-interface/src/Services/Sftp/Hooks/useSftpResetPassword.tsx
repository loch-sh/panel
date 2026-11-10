import { useMutation } from "@tanstack/react-query";
import { sftpService } from "../sftp.service";

export default function useSftpResetPassword() {
  return useMutation({
    mutationFn: sftpService.resetPassword,
  });
}

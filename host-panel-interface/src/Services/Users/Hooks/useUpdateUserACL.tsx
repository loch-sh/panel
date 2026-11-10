import { useMutation, useQueryClient } from "@tanstack/react-query";
import { UsersService, UsersServiceKeys } from "../users.service";

export default function useUpdateUserACL() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: UsersService.updateACL,
    onSettled: () => {
      client.invalidateQueries({
        queryKey: [UsersServiceKeys.users],
      });
    },
  });
}

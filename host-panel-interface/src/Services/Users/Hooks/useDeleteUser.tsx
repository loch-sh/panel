import { useMutation, useQueryClient } from "@tanstack/react-query";
import { UsersService, UsersServiceKeys } from "../users.service";

export default function useDeleteUser() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: UsersService.deleteUser,
    onSettled: () => {
      client.invalidateQueries({
        queryKey: [UsersServiceKeys.users],
      });
    },
  });
}

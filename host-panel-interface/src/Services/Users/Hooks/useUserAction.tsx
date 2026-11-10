import { useMutation, useQueryClient } from "@tanstack/react-query";
import { UsersService, UsersServiceKeys } from "../users.service";

export default function useUserAction() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: UsersService.doAction,
    onSettled: () => {
      client.invalidateQueries({
        queryKey: [UsersServiceKeys.users],
      });
    },
  });
}

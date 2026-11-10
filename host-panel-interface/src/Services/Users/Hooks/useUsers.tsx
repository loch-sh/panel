import { useQuery } from "@tanstack/react-query";
import { UsersService, UsersServiceKeys } from "../users.service";

export default function useUsers() {
  return useQuery({
    queryKey: [UsersServiceKeys.users],
    queryFn: UsersService.list,
  });
}

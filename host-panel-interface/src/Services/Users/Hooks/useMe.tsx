import { useQuery } from "@tanstack/react-query";
import { UsersService, UsersServiceKeys } from "../users.service";
import { secondsToMilliseconds } from "date-fns";

export default function useMe() {
  return useQuery({
    queryKey: [UsersServiceKeys.me],
    queryFn: UsersService.me,
    refetchInterval: secondsToMilliseconds(5),
  });
}

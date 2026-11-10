import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  containersService,
  containersServiceKeys,
} from "../containers.service";

export default function useStackAction() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: containersService.stackAction,
    onSettled: () => {
      client.invalidateQueries({
        queryKey: [containersServiceKeys.containers],
      });
    },
  });
}

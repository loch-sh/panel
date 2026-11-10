import {
  QueryClient,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";
import { networksServiceKeys } from "../../Networks/networks.service";
import { functionsService, functionsServiceKeys } from "../functions.service";

/** After a change: the functions, the budget, and their networks. */
export function invalidateFunctions(client: QueryClient) {
  client.invalidateQueries({ queryKey: [functionsServiceKeys.functions] });
  client.invalidateQueries({ queryKey: [functionsServiceKeys.function] });
  client.invalidateQueries({ queryKey: [functionsServiceKeys.deployments] });
  client.invalidateQueries({ queryKey: [functionsServiceKeys.config] });
}

function useInvalidate(networks = false) {
  const client = useQueryClient();
  return () => {
    invalidateFunctions(client);
    if (networks) {
      // Functions are members of the networks.
      client.invalidateQueries({ queryKey: [networksServiceKeys.networks] });
      client.invalidateQueries({ queryKey: [networksServiceKeys.network] });
    }
  };
}

export function useFunctionCreate() {
  const invalidate = useInvalidate(true);
  return useMutation({
    mutationFn: functionsService.create,
    onSettled: invalidate,
    // The response holds the invoke credentials: not kept in the cache.
    gcTime: 0,
  });
}

export function useFunctionUpdate() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: functionsService.update,
    onSettled: invalidate,
  });
}

export function useFunctionDelete() {
  const invalidate = useInvalidate(true);
  return useMutation({
    mutationFn: functionsService.delete,
    onSettled: invalidate,
  });
}

export function useFunctionAction() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: functionsService.action,
    onSettled: invalidate,
  });
}

export function useFunctionDeploy() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: functionsService.deploy,
    onSettled: invalidate,
  });
}

export function useFunctionActivate() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: functionsService.activate,
    onSettled: invalidate,
  });
}

export function useFunctionSecretSet() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: functionsService.setSecret,
    onSettled: invalidate,
    // The variables hold the value: never kept by React Query.
    gcTime: 0,
  });
}

export function useFunctionSecretDelete() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: functionsService.deleteSecret,
    onSettled: invalidate,
  });
}

export function useFunctionRotateCredentials() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: functionsService.rotateCredentials,
    onSettled: invalidate,
    gcTime: 0,
  });
}

export function useFunctionInvoke() {
  return useMutation({
    mutationFn: functionsService.invoke,
    gcTime: 0,
  });
}

export function useFunctionNetworks() {
  const invalidate = useInvalidate(true);
  return useMutation({
    mutationFn: functionsService.setNetworks,
    onSettled: invalidate,
  });
}

export function useFunctionEgress() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: functionsService.setEgress,
    onSettled: invalidate,
  });
}

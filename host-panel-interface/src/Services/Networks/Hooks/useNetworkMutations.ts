import {
  QueryClient,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";
import { databasesServiceKeys } from "../../Databases/databases.service";
import { functionsServiceKeys } from "../../Functions/functions.service";
import { hostingServiceKeys } from "../../Hosting/hosting.service";
import { networksService, networksServiceKeys } from "../networks.service";

function invalidateNetworks(client: QueryClient) {
  client.invalidateQueries({ queryKey: [networksServiceKeys.networks] });
  client.invalidateQueries({ queryKey: [networksServiceKeys.network] });
  client.invalidateQueries({ queryKey: [networksServiceKeys.config] });
}

/**
 * After a member change: the networks, and the members' side (the networks
 * of the service instances, the spec and operation of the hosted sites).
 */
export function invalidateMembership(client: QueryClient) {
  invalidateNetworks(client);
  client.invalidateQueries({ queryKey: [databasesServiceKeys.instances] });
  client.invalidateQueries({ queryKey: [hostingServiceKeys.stacks] });
  client.invalidateQueries({ queryKey: [hostingServiceKeys.stack] });
  client.invalidateQueries({ queryKey: [functionsServiceKeys.functions] });
  client.invalidateQueries({ queryKey: [functionsServiceKeys.function] });
}

export function useNetworkCreate() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: networksService.create,
    onSettled: () => invalidateNetworks(client),
  });
}

export function useNetworkUpdate() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: networksService.update,
    onSettled: () => invalidateNetworks(client),
  });
}

export function useNetworkDelete() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: networksService.delete,
    onSettled: () => invalidateNetworks(client),
  });
}

export function useNetworkAttach() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: networksService.attach,
    onSettled: () => invalidateMembership(client),
  });
}

export function useNetworkDetach() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: networksService.detach,
    onSettled: () => invalidateMembership(client),
  });
}

export function useNetworkDisconnect() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: networksService.disconnect,
    onSettled: () => invalidateNetworks(client),
  });
}

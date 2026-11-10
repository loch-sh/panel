import { useMutation, useQueryClient } from "@tanstack/react-query";
import { networksServiceKeys } from "../../Networks/networks.service";
import { databasesService, databasesServiceKeys } from "../databases.service";

function useInvalidate() {
  const client = useQueryClient();
  return () => {
    client.invalidateQueries({ queryKey: [databasesServiceKeys.instances] });
    client.invalidateQueries({ queryKey: [databasesServiceKeys.resources] });
    // Instances are members of the networks.
    client.invalidateQueries({ queryKey: [networksServiceKeys.networks] });
    client.invalidateQueries({ queryKey: [networksServiceKeys.network] });
  };
}

export function useDatabaseCreate() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: databasesService.create,
    onSettled: invalidate,
    // The response holds the password: do not keep it in the cache.
    gcTime: 0,
  });
}

export function useDatabaseDelete() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: databasesService.delete,
    onSettled: invalidate,
  });
}

export function useDatabaseRotatePassword() {
  // Rotating a password changes neither instances nor resources: nothing to refetch.
  return useMutation({
    mutationFn: databasesService.rotatePassword,
    gcTime: 0,
  });
}

export function useInstanceRetire() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: databasesService.retire,
    onSettled: invalidate,
  });
}

/** Changes the options of a dedicated instance (it is recreated). */
export function useInstanceUpdate() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: databasesService.update,
    onSettled: invalidate,
  });
}

/** Deletes an instance edited by hand, with its data. */
export function useInstancePurge() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: databasesService.purge,
    onSettled: invalidate,
  });
}

/** Replaces the networks of an instance (it is recreated). */
export function useInstanceNetworksUpdate() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: databasesService.updateNetworks,
    onSettled: invalidate,
  });
}

/** Publishes or closes the port of an instance (it is recreated). */
export function useInstanceExposure() {
  const invalidate = useInvalidate();
  const set = useMutation({
    mutationFn: databasesService.setExposure,
    onSettled: invalidate,
  });
  const close = useMutation({
    mutationFn: databasesService.deleteExposure,
    onSettled: invalidate,
  });
  return { set, close };
}

/** Updates a dedicated instance (new image or newer version; restarted). */
export function useInstanceUpgrade() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: databasesService.upgrade,
    onSettled: invalidate,
  });
}

/** Checks the registries for new versions now. */
export function useCheckServiceUpdates() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: databasesService.checkUpdates,
    onSettled: () => {
      client.invalidateQueries({ queryKey: [databasesServiceKeys.config] });
      client.invalidateQueries({ queryKey: [databasesServiceKeys.instances] });
    },
  });
}

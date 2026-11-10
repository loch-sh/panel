import { QueryClient } from "@tanstack/react-query";
import { containersServiceKeys } from "../Containers/containers.service";
import { sftpServiceKeys } from "../Sftp/sftp.service";
import { hostingServiceKeys } from "./hosting.service";

/**
 * Refreshes the data a finished hosting operation changes: containers,
 * hosting stacks and sites, and the SFTP sites (a purge removes a directory).
 */
export function invalidateAfterOperation(client: QueryClient) {
  client.invalidateQueries({ queryKey: [containersServiceKeys.containers] });
  client.invalidateQueries({ queryKey: [hostingServiceKeys.stacks] });
  client.invalidateQueries({ queryKey: [hostingServiceKeys.sites] });
  client.invalidateQueries({ queryKey: [sftpServiceKeys.sites] });
}

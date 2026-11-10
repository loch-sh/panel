import { useQuery } from "@tanstack/react-query";
import { hostingService, hostingServiceKeys } from "../hosting.service";
import { HostingTemplate, templateHasPhp } from "../Models/hosting.model";

const ONE_HOUR_MS = 60 * 60 * 1000;

/**
 * Modules of a PHP image. The first call for an image may take a minute or
 * two (the API pulls it), so results are kept for an hour and not retried.
 */
export default function useHostingPhpModules(
  template: HostingTemplate | undefined,
  version: string | undefined,
) {
  return useQuery({
    queryFn: () => hostingService.phpModules(template!, version!),
    queryKey: [hostingServiceKeys.phpModules, template, version],
    enabled: templateHasPhp(template) && !!version,
    staleTime: ONE_HOUR_MS,
    retry: false,
  });
}

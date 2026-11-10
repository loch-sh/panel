import { formatDistance } from "date-fns";
import { useCallback } from "react";
import { useTranslation } from "react-i18next";
import { isContainerState, parseDockerStatus } from "../Utils/dockerStatus";
import { useDateFnsLocale } from "./useLocales";

/**
 * Translated labels for Docker container states and statuses.
 *
 * - `stateLabel("exited")` → "Arrêté"
 * - `statusLabel("Exited (0) 5 days ago")` → "Arrêté (0) il y a 5 jours"
 * - `statusLabel("Up 3 hours (healthy)")` → "Démarré il y a 3 heures (sain)"
 *
 * Unknown values are returned unchanged.
 */
export function useContainerLabels() {
  const { t } = useTranslation("common");
  const locale = useDateFnsLocale();

  const stateLabel = useCallback(
    (state: string): string =>
      isContainerState(state) ? t(`container.state.${state}`) : state,
    [t],
  );

  const statusLabel = useCallback(
    (status: string): string => {
      const parsed = parseDockerStatus(status);
      if (!parsed) return status;

      const now = Date.now();
      const ago =
        parsed.elapsedMs === undefined
          ? ""
          : formatDistance(now - parsed.elapsedMs, now, {
              addSuffix: true,
              locale,
            });
      const code = String(parsed.exitCode ?? "");

      let label: string;
      switch (parsed.kind) {
        case "up":
          label = t("container.status.up", { ago });
          break;
        case "paused":
          label = t("container.status.paused", { ago });
          break;
        case "exited":
          label = t("container.status.exited", { code, ago });
          break;
        case "restarting":
          label = t("container.status.restarting", { code, ago });
          break;
        case "created":
        case "removing":
        case "dead":
          label = t(`container.state.${parsed.kind}`);
          break;
      }

      return parsed.health
        ? t("container.status.withHealth", {
            status: label,
            health: t(`container.health.${parsed.health}`),
          })
        : label;
    },
    [t, locale],
  );

  return { stateLabel, statusLabel };
}

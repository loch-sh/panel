import { Flex, Typography } from "antd";
import type { TFunction } from "i18next";
import { ServiceInstanceModel } from "../../Services/Databases/Models/databases.model";
import {
  FunctionSummaryModel,
  needsPurge,
} from "../../Services/Functions/Models/functions.model";
import { HostingStackSummary } from "../../Services/Hosting/Models/hosting.model";
import { AttachableKind } from "../../Services/Networks/Models/networks.model";
import { engineLabel } from "../Databases/serviceLabels";

const { Text } = Typography;

export interface AttachTarget {
  kind: AttachableKind;
  name: string;
}

const SEPARATOR = "/";
export const encodeMember = (kind: AttachableKind, name: string) =>
  `${kind}${SEPARATOR}${name}`;
export function decodeMember(value: string): AttachTarget {
  const i = value.indexOf(SEPARATOR);
  return {
    kind: value.slice(0, i) as AttachableKind,
    name: value.slice(i + 1),
  };
}

/**
 * Sites and service instances offered to join a network, grouped, with the
 * reason a member cannot join (edited by hand, operation running, or an
 * engine without authentication on the shared network).
 */
export function memberOptions(
  t: TFunction<"networks">,
  tdb: TFunction<"databases">,
  stacks: readonly HostingStackSummary[],
  instances: readonly ServiceInstanceModel[],
  { system, declared }: { system: boolean; declared: ReadonlySet<string> },
  functions: readonly FunctionSummaryModel[] = [],
) {
  const sites = stacks
    .filter((s) => !declared.has(encodeMember("site", s.name)))
    .map((s) => {
      const busy = s.operation?.state === "running";
      const reason = s.modified
        ? t("attach.disabled.modified")
        : busy
          ? t("attach.disabled.operation")
          : undefined;
      return {
        value: encodeMember("site", s.name),
        disabled: !!reason,
        title: s.name,
        label: (
          <Flex gap={6} wrap>
            <span>{s.name}</span>
            {reason && <Text type="secondary">({reason})</Text>}
          </Flex>
        ),
      };
    });
  const insts = instances
    .filter((i) => !declared.has(encodeMember("instance", i.key)))
    .map((i) => {
      const title = i.name
        ? `${engineLabel(tdb, i.engine)} ${i.name}`
        : `${engineLabel(tdb, i.engine)} ${i.version}`;
      const reason = i.modified
        ? t("attach.disabled.modified")
        : system && i.unauthenticated
          ? t("attach.disabled.unauthenticated")
          : undefined;
      return {
        value: encodeMember("instance", i.key),
        disabled: !!reason,
        title,
        label: (
          <Flex gap={6} wrap>
            <span>{title}</span>
            <Text type="secondary">{i.project}</Text>
            {reason && <Text type="secondary">({reason})</Text>}
          </Flex>
        ),
      };
    });
  // A function never joins the shared network of the services module.
  const fns = system
    ? []
    : functions
        .filter((f) => !declared.has(encodeMember("function", f.name)))
        .map((f) => {
          const reason = needsPurge(f.state)
            ? t("attach.disabled.modified")
            : undefined;
          return {
            value: encodeMember("function", f.name),
            disabled: !!reason,
            title: f.name,
            label: (
              <Flex gap={6} wrap>
                <span>{f.name}</span>
                <Text type="secondary">{`fn-${f.name}`}</Text>
                {reason && <Text type="secondary">({reason})</Text>}
              </Flex>
            ),
          };
        });
  return [
    ...(sites.length > 0
      ? [{ label: t("attach.sites"), title: "sites", options: sites }]
      : []),
    ...(insts.length > 0
      ? [{ label: t("attach.instances"), title: "instances", options: insts }]
      : []),
    ...(fns.length > 0
      ? [{ label: t("attach.functions"), title: "functions", options: fns }]
      : []),
  ];
}

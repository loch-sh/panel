import { Alert, Card, Descriptions, Flex, Tag, Typography } from "antd";
import { useTranslation } from "react-i18next";
import {
  FunctionDetailModel,
  splitImage,
} from "../../../Services/Functions/Models/functions.model";
import { NetworkLinks } from "../../Networks/NetworkTags";
import {
  EgressTag,
  formatDateTime,
  formatDeploymentDate,
  formatMB,
  FunctionStateBadge,
  runtimeVersionLabel,
} from "../functionLabels";

const { Text } = Typography;

/** State, address, image and limits of a function. */
export default function OverviewTab({
  fn,
}: Readonly<{ fn: FunctionDetailModel }>) {
  const { t, i18n } = useTranslation("functions");
  const lang = i18n.resolvedLanguage;
  const image = splitImage(fn.image ?? "");
  const active = fn.deployments?.find((d) => d.active);

  return (
    <Flex vertical gap="middle">
      {fn.state === "modified" && (
        <Alert
          type="error"
          showIcon
          title={t("states.modified")}
          description={t("overview.modified")}
        />
      )}
      {fn.state === "invalid" && (
        <Alert
          type="error"
          showIcon
          title={t("states.invalid")}
          description={t("overview.invalid")}
        />
      )}
      {fn.state === "unverified" && (
        <Alert
          type="error"
          showIcon
          title={t("states.unverified")}
          description={
            <Flex vertical gap={4}>
              <Text>{t("overview.unverified")}</Text>
              {fn.problem && (
                // Server wording, technical: shown as text.
                <Text code>{fn.problem}</Text>
              )}
            </Flex>
          }
        />
      )}
      {fn.state === "not_deployed" && (
        <Alert type="info" showIcon title={t("overview.notDeployed")} />
      )}
      {fn.state === "missing" && (
        <Alert type="warning" showIcon title={t("stateHints.missing")} />
      )}

      <Card title={t("overview.invocation")}>
        <Descriptions
          column={1}
          size="small"
          items={[
            {
              key: "endpoint",
              label: t("overview.endpoint"),
              children: fn.endpoint ? (
                <Text code copyable style={{ wordBreak: "break-all" }}>
                  {fn.endpoint}
                </Text>
              ) : (
                "—"
              ),
            },
            {
              key: "auth",
              label: t("overview.auth"),
              children: fn.sigv4
                ? t("create.summary.authSigv4")
                : t("create.summary.authBearer"),
            },
            {
              key: "networks",
              label: t("overview.networks"),
              children: <NetworkLinks networks={fn.networks} />,
            },
            {
              key: "egress",
              label: t("overview.internet"),
              children: <EgressTag egress={fn.egress} />,
            },
          ]}
        />
        <Text type="secondary">{t("overview.endpointHint")}</Text>
      </Card>

      <Card title={t("overview.runtimeTitle")}>
        <Descriptions
          column={{ xs: 1, md: 2 }}
          size="small"
          items={[
            {
              key: "state",
              label: t("overview.state"),
              children: (
                <FunctionStateBadge state={fn.state} health={fn.health} />
              ),
            },
            {
              key: "runtime",
              label: t("overview.runtime"),
              children: fn.runtime
                ? `${runtimeVersionLabel(t, fn.runtime, fn.runtimeVersion)} (${fn.arch})`
                : "—",
            },
            {
              key: "handler",
              label: t("overview.handler"),
              children: fn.handler ? <Text code>{fn.handler}</Text> : "—",
            },
            {
              key: "deployment",
              label: t("overview.deployment"),
              children: active ? (
                <Flex gap={4} wrap>
                  <Text code>{active.id}</Text>
                  <Text type="secondary">
                    {formatDeploymentDate(active.id, lang)}
                  </Text>
                </Flex>
              ) : (
                <Text type="secondary">{t("noCode")}</Text>
              ),
            },
            {
              key: "image",
              label: t("overview.image"),
              span: { xs: 1, md: 2 },
              children: image.ref ? (
                <Text
                  code
                  copyable={{ text: fn.image }}
                  style={{ wordBreak: "break-all" }}
                >
                  {image.ref}
                </Text>
              ) : (
                "—"
              ),
            },
            {
              key: "digest",
              label: t("overview.digest"),
              span: { xs: 1, md: 2 },
              children: image.digest ? (
                <Text code style={{ wordBreak: "break-all" }}>
                  {image.digest}
                </Text>
              ) : (
                "—"
              ),
            },
            {
              key: "created",
              label: t("overview.created"),
              children: formatDateTime(fn.createdAt, lang),
            },
          ]}
        />
      </Card>

      <Card title={t("overview.limits")}>
        <Flex gap="small" wrap>
          <Tag>
            {t("overview.limitMemory", { value: formatMB(fn.memoryMB) })}
          </Tag>
          <Tag>{t("overview.limitCpus", { value: fn.cpus })}</Tag>
          <Tag>
            {t("overview.limitTimeout", {
              value: t("units.seconds", { count: fn.timeoutSeconds }),
            })}
          </Tag>
          <Tag>{t("overview.limitTmp", { value: formatMB(fn.tmpSizeMB) })}</Tag>
        </Flex>
      </Card>
    </Flex>
  );
}

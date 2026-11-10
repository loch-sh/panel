import { Flex, Tag, Typography } from "antd";
import type { DescriptionsProps } from "antd";
import { useTranslation } from "react-i18next";
import {
  HostingStackPayload,
  templateHasPhp,
} from "../../../../Services/Hosting/Models/hosting.model";
import PhpMemorySummary from "../../Detail/PhpMemorySummary";
import ResourcesSummary from "../../Detail/ResourcesSummary";
import RoutingSummary from "../../Detail/RoutingSummary";
import DomainLinks from "../../DomainLinks";
import TemplateTag from "../../TemplateTag";
import { SectionKey } from "../sections/sectionKeys";

type Items = NonNullable<DescriptionsProps["items"]>;

const { Text } = Typography;

/** Description items of the review, per section, from the payload to send. */
export default function useReviewItems(
  payload: HostingStackPayload,
  sitesDirectory?: string,
): Record<SectionKey, Items> {
  const { t } = useTranslation("hosting");
  const yesNo = (value: boolean) =>
    value
      ? t("actions.yes", { ns: "common" })
      : t("actions.no", { ns: "common" });
  const filled = (text?: string) =>
    text?.trim()
      ? t("form.layout.summary.configured")
      : t("form.layout.summary.empty");
  const php = templateHasPhp(payload.template) ? payload.php : undefined;
  const disabled = php?.disabledModules ?? [];
  const sitePath = sitesDirectory
    ? `${sitesDirectory}/${payload.site}`
    : payload.site;

  return {
    type: [
      {
        key: "template",
        label: t("detail.overview.template"),
        children: <TemplateTag template={payload.template} />,
      },
    ],
    site: [
      {
        key: "name",
        label: t("form.fields.name.label"),
        children: payload.name,
      },
      {
        key: "site",
        label: t("detail.overview.sitePath"),
        children: <Text code>{sitePath}</Text>,
      },
      {
        key: "docroot",
        label: t("detail.overview.documentRoot"),
        children: payload.documentRoot ? (
          <Text code>{payload.documentRoot}</Text>
        ) : (
          <Text type="secondary">{t("detail.overview.documentRootRoot")}</Text>
        ),
      },
      {
        key: "domains",
        label: t("detail.overview.domains"),
        children: (
          <DomainLinks
            domains={payload.domains}
            https={payload.https}
            vertical
          />
        ),
      },
      {
        key: "https",
        label: t("detail.overview.https"),
        children: yesNo(payload.https),
      },
      {
        key: "redirect",
        label: t("detail.overview.redirectHttps"),
        children: yesNo(payload.redirectHttps),
      },
      {
        key: "networks",
        label: t("detail.overview.networks"),
        children: payload.networks?.length ? (
          <Flex gap={4} wrap>
            {payload.networks.map((n) => (
              <Tag key={n}>{n}</Tag>
            ))}
          </Flex>
        ) : (
          t("detail.overview.noNetwork")
        ),
      },
    ],
    runtime: [
      ...(php
        ? [
            {
              key: "phpVersion",
              label: t("detail.overview.phpVersion"),
              children: payload.phpVersion,
            },
            {
              key: "phpMemory",
              label: t("detail.overview.phpMemory"),
              children: <PhpMemorySummary php={php} />,
            },
            {
              key: "maxExecutionTime",
              label: t("form.fields.maxExecutionTime.label"),
              children: t("form.layout.review.seconds", {
                value: php.maxExecutionTime,
              }),
            },
            {
              key: "timezone",
              label: t("form.fields.timezone.label"),
              children: php.timezone,
            },
            {
              key: "disabledModules",
              label: t("detail.overview.disabledModules"),
              children: disabled.length ? (
                <Flex gap={4} wrap>
                  {disabled.map((m) => (
                    <Tag key={m}>{m}</Tag>
                  ))}
                </Flex>
              ) : (
                t("form.layout.summary.allEnabled")
              ),
            },
          ]
        : []),
      {
        key: "resources",
        label: t("detail.overview.resources"),
        children: <ResourcesSummary resources={payload.resources} />,
      },
    ],
    advanced: [
      {
        key: "routing",
        label: t("detail.overview.routing"),
        children: <RoutingSummary spec={payload} />,
      },
      {
        key: "env",
        label: t("form.fields.env.label"),
        children: t("form.layout.summary.variables", {
          count: payload.env.length,
        }),
      },
      ...(php
        ? [
            {
              key: "phpIni",
              label: t("form.fields.phpIni.label"),
              children: filled(payload.phpIni),
            },
          ]
        : []),
      {
        key: "vhost",
        label: t("form.fields.vhost.label"),
        children: filled(payload.vhost),
      },
    ],
  };
}

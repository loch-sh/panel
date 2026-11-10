import { Card, Descriptions, Flex, Tag, Typography } from "antd";
import { useTranslation } from "react-i18next";
import {
  HostingStackDetail,
  templateHasPhp,
} from "../../../../Services/Hosting/Models/hosting.model";
import { NetworkLinks } from "../../../Networks/NetworkTags";
import DomainLinks from "../../DomainLinks";
import PhpVersionTag from "../../PhpVersionTag";
import TemplateTag from "../../TemplateTag";
import PhpMemorySummary from "../PhpMemorySummary";
import ResourcesSummary from "../ResourcesSummary";
import RoutingSummary from "../RoutingSummary";

const { Text } = Typography;

interface Props {
  stack: HostingStackDetail;
  sitesDirectory?: string;
  phpDeprecated: boolean;
}

/** Settings of the stack (template, PHP, routing, site, domains, limits). */
export default function OverviewDescriptions({
  stack,
  sitesDirectory,
  phpDeprecated,
}: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  const sitePath = sitesDirectory
    ? `${sitesDirectory}/${stack.site}`
    : stack.site;
  const documentRoot = stack.spec?.documentRoot ?? "";
  const resources = stack.spec?.resources;

  return (
    <Card size="small">
      <Descriptions
        bordered
        size="small"
        column={{ xs: 1, md: 2 }}
        items={[
          {
            key: "template",
            label: t("detail.overview.template"),
            children: <TemplateTag template={stack.template} />,
          },
          {
            key: "image",
            label: t("detail.overview.image"),
            children: <Text code>{stack.image}</Text>,
          },
          ...(templateHasPhp(stack.template)
            ? [
                {
                  key: "php",
                  label: t("detail.overview.phpVersion"),
                  children: (
                    <PhpVersionTag
                      version={stack.phpVersion}
                      deprecated={phpDeprecated}
                    />
                  ),
                },
                // Only when the settings are known (a stack without a
                // readable spec would show an empty cell).
                ...(stack.spec?.php
                  ? [
                      {
                        key: "phpMemory",
                        label: t("detail.overview.phpMemory"),
                        children: <PhpMemorySummary php={stack.spec.php} />,
                      },
                    ]
                  : []),
              ]
            : []),
          ...(templateHasPhp(stack.template)
            ? [
                {
                  key: "disabledModules",
                  label: t("detail.overview.disabledModules"),
                  children: stack.spec?.php?.disabledModules?.length ? (
                    <Flex gap={4} wrap>
                      {stack.spec.php.disabledModules.map((m) => (
                        <Tag key={m}>{m}</Tag>
                      ))}
                    </Flex>
                  ) : (
                    <Text type="secondary">
                      {t("detail.overview.noDisabledModules")}
                    </Text>
                  ),
                },
              ]
            : []),
          {
            key: "routing",
            label: t("detail.overview.routing"),
            children: <RoutingSummary spec={stack.spec} />,
          },
          {
            key: "site",
            label: t("detail.overview.sitePath"),
            children: (
              <Text code copyable>
                {sitePath}
              </Text>
            ),
          },
          {
            key: "docroot",
            label: t("detail.overview.documentRoot"),
            children: documentRoot ? (
              <Text code>{documentRoot}</Text>
            ) : (
              <Text type="secondary">
                {t("detail.overview.documentRootRoot")}
              </Text>
            ),
          },
          {
            key: "domains",
            label: t("detail.overview.domains"),
            children: (
              <DomainLinks
                domains={stack.domains}
                https={stack.https}
                vertical
              />
            ),
          },
          {
            key: "https",
            label: t("detail.overview.https"),
            children: stack.https
              ? t("actions.yes", { ns: "common" })
              : t("actions.no", { ns: "common" }),
          },
          {
            key: "redirect",
            label: t("detail.overview.redirectHttps"),
            children: stack.redirectHttps
              ? t("actions.yes", { ns: "common" })
              : t("actions.no", { ns: "common" }),
          },
          ...(stack.spec
            ? [
                {
                  key: "networks",
                  label: t("detail.overview.networks"),
                  children: stack.spec.networks?.length ? (
                    <NetworkLinks networks={stack.spec.networks} />
                  ) : (
                    t("detail.overview.noNetwork")
                  ),
                },
              ]
            : []),
          ...(resources
            ? [
                {
                  key: "resources",
                  label: t("detail.overview.resources"),
                  children: <ResourcesSummary resources={resources} />,
                },
              ]
            : []),
        ]}
      />
    </Card>
  );
}

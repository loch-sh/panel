import { Alert, Flex } from "antd";
import { useTranslation } from "react-i18next";
import EnvVarsField from "../EnvVarsField";
import PhpIniField from "../PhpIniField";
import RoutingFields from "../Routing/RoutingFields";
import VhostField from "../VhostField";
import FieldPanels, { FieldPanel } from "./FieldPanels";
import { SectionContentProps } from "./sectionProps";

/**
 * Optional settings, each in a panel closed by default: routing, environment
 * variables, php.ini and vhost snippets.
 */
export default function AdvancedSection({
  limits,
  hasPhp,
  usesNginx,
  resetKey,
  onPrefillingChange,
  errorFields,
  failures,
  summaries,
}: Readonly<SectionContentProps>) {
  const { t } = useTranslation("hosting");

  const panels: FieldPanel[] = [
    {
      key: "routing",
      title: t("form.sections.routing"),
      summary: summaries.routing,
      content: (
        <RoutingFields
          key={resetKey}
          maxBytes={limits.maxSnippetBytes}
          onPrefillingChange={onPrefillingChange}
        />
      ),
    },
    {
      key: "env",
      title: t("form.fields.env.label"),
      summary: summaries.env,
      content: <EnvVarsField maxEnvVars={limits.maxEnvVars} />,
    },
    ...(hasPhp
      ? [
          {
            key: "phpIni" as const,
            title: t("form.fields.phpIni.label"),
            summary: summaries.phpIni,
            content: <PhpIniField maxBytes={limits.maxSnippetBytes} />,
          },
        ]
      : []),
    {
      key: "vhost",
      title: t("form.fields.vhost.label"),
      summary: summaries.vhost,
      content: (
        <VhostField usesNginx={usesNginx} maxBytes={limits.maxSnippetBytes} />
      ),
    },
  ];

  return (
    <Flex vertical gap="middle">
      <Alert type="info" showIcon title={t("form.layout.advancedNotice")} />
      <FieldPanels
        panels={panels}
        errorFields={errorFields}
        failures={failures}
      />
    </Flex>
  );
}

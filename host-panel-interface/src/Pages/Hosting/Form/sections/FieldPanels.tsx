import { Collapse, Flex, Typography } from "antd";
import { ReactNode, useState } from "react";
import { PanelKey, panelOfField } from "./sectionKeys";

export interface FieldPanel {
  key: PanelKey;
  title: string;
  /** short state shown in the header, e.g. "3 variables" */
  summary: string;
  content: ReactNode;
}

interface Props {
  panels: FieldPanel[];
  errorFields: (string | number)[][];
  /** failed saves / steps so far: reopens the panels in error each time */
  failures: number;
}

/**
 * Collapsible groups of optional fields, closed by default. The fields stay
 * mounted when closed (validation, server errors), and a panel holding a
 * field in error is opened.
 */
export default function FieldPanels({
  panels,
  errorFields,
  failures,
}: Readonly<Props>) {
  const [opened, setOpened] = useState<string[]>([]);
  // Panels holding an error are opened when the errors change, and can be
  // closed again afterwards.
  const withErrors = [
    ...new Set(
      errorFields.map(panelOfField).filter((key): key is PanelKey => !!key),
    ),
  ];
  const errorKey = `${failures}:${withErrors.join(",")}`;
  const [prevErrorKey, setPrevErrorKey] = useState(errorKey);
  if (errorKey !== prevErrorKey) {
    setPrevErrorKey(errorKey);
    setOpened((keys) => [...new Set([...keys, ...withErrors])]);
  }

  return (
    <Collapse
      activeKey={opened}
      onChange={(keys) => setOpened(Array.isArray(keys) ? keys : [keys])}
      items={panels.map((panel) => ({
        key: panel.key,
        forceRender: true,
        label: (
          <Flex justify="space-between" gap="small" wrap>
            <Typography.Text strong>{panel.title}</Typography.Text>
            <Typography.Text type="secondary">{panel.summary}</Typography.Text>
          </Flex>
        ),
        children: panel.content,
      }))}
    />
  );
}

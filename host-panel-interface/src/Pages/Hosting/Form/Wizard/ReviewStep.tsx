import { CheckCircleOutlined, EditOutlined } from "@ant-design/icons";
import { Button, Card, Descriptions, Flex, Form, Typography } from "antd";
import { useTranslation } from "react-i18next";
import { formValuesToPayload, HostingFormValues } from "../formValues";
import SectionHeader from "../sections/SectionHeader";
import { SectionDef } from "../sections/useSections";
import useReviewItems from "./useReviewItems";

interface Props {
  sections: SectionDef[];
  sitesDirectory?: string;
  /** jumps back to the step of a section */
  onEdit: (step: number) => void;
}

/** Summary of what will be created, section by section, with edit links. */
export default function ReviewStep({
  sections,
  sitesDirectory,
  onEdit,
}: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  const form = Form.useFormInstance<HostingFormValues>();
  // Re-rendered on any change; the whole store, like the submission.
  const values = Form.useWatch((all: HostingFormValues) => all, {
    form,
    preserve: true,
  });
  const items = useReviewItems(
    formValuesToPayload(values ?? form.getFieldsValue(true)),
    sitesDirectory,
  );

  return (
    <Card
      title={
        <SectionHeader
          icon={<CheckCircleOutlined />}
          title={t("form.layout.review.title")}
          subtitle={t("form.layout.review.subtitle")}
        />
      }
    >
      <Flex vertical gap="large">
        {sections.map((section, index) => (
          <Descriptions
            key={section.key}
            bordered
            size="small"
            column={{ xs: 1, md: 2 }}
            title={
              <Typography.Text strong>
                {section.icon} {section.title}
              </Typography.Text>
            }
            extra={
              <Button
                type="link"
                size="small"
                icon={<EditOutlined />}
                onClick={() => onEdit(index)}
              >
                {t("form.layout.edit")}
              </Button>
            }
            items={items[section.key]}
          />
        ))}
      </Flex>
    </Card>
  );
}

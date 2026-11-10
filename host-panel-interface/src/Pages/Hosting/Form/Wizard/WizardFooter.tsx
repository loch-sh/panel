import { ArrowLeftOutlined, ArrowRightOutlined } from "@ant-design/icons";
import { Button, Flex } from "antd";
import { ReactNode } from "react";
import { useTranslation } from "react-i18next";

interface Props {
  step: number;
  lastStep: number;
  onPrevious: () => void;
  onNext: () => void;
  onSubmit: () => void;
  submitLabel: string;
  submitting: boolean;
  submitDisabled: boolean;
  extraActions?: ReactNode;
}

/** Previous / Next buttons, and the submit button on the last step. */
export default function WizardFooter({
  step,
  lastStep,
  onPrevious,
  onNext,
  onSubmit,
  submitLabel,
  submitting,
  submitDisabled,
  extraActions,
}: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  return (
    <Flex justify="space-between" gap="small" wrap>
      <Flex gap="small">{extraActions}</Flex>
      <Flex gap="small">
        {step > 0 && (
          <Button icon={<ArrowLeftOutlined />} onClick={onPrevious}>
            {t("form.layout.previous")}
          </Button>
        )}
        {step < lastStep ? (
          <Button
            type="primary"
            icon={<ArrowRightOutlined />}
            iconPlacement="end"
            onClick={onNext}
          >
            {t("form.layout.next")}
          </Button>
        ) : (
          <Button
            type="primary"
            loading={submitting}
            disabled={submitDisabled}
            onClick={onSubmit}
          >
            {submitLabel}
          </Button>
        )}
      </Flex>
    </Flex>
  );
}

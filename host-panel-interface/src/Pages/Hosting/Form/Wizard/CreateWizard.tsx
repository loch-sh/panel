import { CheckCircleOutlined } from "@ant-design/icons";
import { Card, Flex, Grid, Steps, theme } from "antd";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import useScrollToFirstError from "../useScrollToFirstError";
import SectionHeader from "../sections/SectionHeader";
import { fieldsOfSection } from "../sections/sectionKeys";
import { FormLayoutProps } from "./layoutProps";
import ReviewStep from "./ReviewStep";
import WizardFooter from "./WizardFooter";

interface Props extends FormLayoutProps {
  sitesDirectory?: string;
}

/**
 * Creation as a step wizard: one section per step, then a review. Every
 * section stays mounted (hidden when not current) so validation, values and
 * server errors work across steps.
 */
export default function CreateWizard({
  form,
  sections,
  errorSections,
  submit,
  submitLabel,
  submitting,
  submitDisabled,
  extraActions,
  reportFailure,
  sitesDirectory,
}: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  const { token } = theme.useToken();
  const screens = Grid.useBreakpoint();
  const [step, setStep] = useState(0);
  const [reached, setReached] = useState(0);
  const reviewStep = sections.length;
  const top = useRef<HTMLElement>(null);
  const scrollToFirstError = useScrollToFirstError(form);

  /** Shows a step; `scrollTop`: brings the steps into view (navigation). */
  const goTo = (index: number, scrollTop = true) => {
    setStep(index);
    setReached((r) => Math.max(r, index));
    if (scrollTop) {
      requestAnimationFrame(() =>
        top.current?.scrollIntoView({ block: "start", behavior: "smooth" }),
      );
    }
  };

  const next = async () => {
    const section = sections[step];
    const names = fieldsOfSection(form, section.key);
    try {
      await form.validateFields(names);
    } catch {
      reportFailure();
      scrollToFirstError(names); // errors are shown on the fields
      return;
    }
    goTo(step + 1);
  };

  const create = async () => {
    if (await submit()) return;
    // Validation or server error: show the first step holding one.
    const index = sections.findIndex((section) =>
      form
        .getFieldsError(fieldsOfSection(form, section.key))
        .some((f) => f.errors.length > 0),
    );
    if (index >= 0) {
      goTo(index, false);
      scrollToFirstError();
    }
  };

  const items = [
    ...sections.map((section, index) => ({
      title: section.title,
      icon: screens.md ? undefined : section.icon,
      status: errorSections.has(section.key) ? ("error" as const) : undefined,
      disabled: index > reached && !errorSections.has(section.key),
    })),
    {
      title: t("form.layout.review.title"),
      icon: screens.md ? undefined : <CheckCircleOutlined />,
      disabled: reviewStep > reached,
    },
  ];

  return (
    <Flex vertical gap={token.marginLG}>
      <nav ref={top} aria-label={t("form.layout.stepsLabel")}>
        <Steps
          current={step}
          onChange={(index) => goTo(index)}
          items={items}
          size={screens.md ? "default" : "small"}
          type={screens.md ? "default" : "inline"}
        />
      </nav>

      {sections.map((section, index) => (
        <Card
          key={section.key}
          hidden={index !== step}
          title={
            <SectionHeader
              icon={section.icon}
              title={section.title}
              subtitle={section.subtitle}
            />
          }
        >
          {section.content}
        </Card>
      ))}

      {step === reviewStep && (
        <ReviewStep
          sections={sections}
          sitesDirectory={sitesDirectory}
          onEdit={goTo}
        />
      )}

      <WizardFooter
        step={step}
        lastStep={reviewStep}
        onPrevious={() => goTo(step - 1)}
        onNext={() => void next()}
        onSubmit={() => void create()}
        submitLabel={submitLabel}
        submitting={submitting}
        submitDisabled={submitDisabled}
        extraActions={extraActions}
      />
    </Flex>
  );
}

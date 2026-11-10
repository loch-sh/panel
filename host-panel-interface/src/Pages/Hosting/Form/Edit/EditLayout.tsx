import { Anchor, Button, Card, Col, Flex, Grid, Row, theme } from "antd";
import { useTranslation } from "react-i18next";
import useScrollToFirstError from "../useScrollToFirstError";
import SectionHeader from "../sections/SectionHeader";
import { FormLayoutProps } from "../Wizard/layoutProps";
import UnsavedBar from "./UnsavedBar";

interface Props extends FormLayoutProps {
  readOnly: boolean;
  /** values changed since the last load or save */
  dirty: boolean;
  /** back to the saved values */
  onDiscard: () => void;
}

const sectionId = (key: string) => `hosting-section-${key}`;

/**
 * Edition: every section as a card in one column, a sticky side navigation
 * on desktop, and a save bar that only shows when there are changes.
 */
export default function EditLayout({
  form,
  sections,
  errorSections,
  submit,
  submitLabel,
  submitting,
  submitDisabled,
  extraActions,
  readOnly,
  dirty,
  onDiscard,
}: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  const { token } = theme.useToken();
  const screens = Grid.useBreakpoint();
  const scrollToFirstError = useScrollToFirstError(form);

  const save = async () => {
    // A collapsed panel holding the error opens first, then it is scrolled to.
    if (!(await submit())) scrollToFirstError();
  };

  return (
    <>
      <Row gutter={token.marginLG}>
        <Col xs={24} lg={19}>
          <Flex vertical gap={token.margin}>
            {sections.map((section) => (
              <Card
                key={section.key}
                id={sectionId(section.key)}
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
          </Flex>
        </Col>
        {screens.lg && (
          <Col lg={5}>
            <nav aria-label={t("form.layout.navigation")}>
              <Anchor
                offsetTop={token.marginLG}
                items={sections.map((section) => ({
                  key: section.key,
                  href: `#${sectionId(section.key)}`,
                  title: (
                    <span
                      style={{
                        color: errorSections.has(section.key)
                          ? token.colorError
                          : undefined,
                      }}
                    >
                      {section.title}
                    </span>
                  ),
                }))}
              />
            </nav>
          </Col>
        )}
      </Row>

      {!readOnly && dirty && (
        <UnsavedBar
          onSave={() => void save()}
          saveLabel={submitLabel}
          saving={submitting}
          saveDisabled={submitDisabled}
          extraActions={
            <>
              {extraActions}
              <Button
                onClick={onDiscard}
                disabled={submitting || submitDisabled}
              >
                {t("settings.reset")}
              </Button>
            </>
          }
        />
      )}
    </>
  );
}

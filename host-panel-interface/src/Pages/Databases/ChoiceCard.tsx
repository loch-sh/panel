import { CheckCircleFilled } from "@ant-design/icons";
import { Card, Col, Flex, Row, Typography, theme } from "antd";
import { KeyboardEvent, ReactNode, memo, useRef } from "react";

export interface Choice {
  value: string;
  title: ReactNode;
  description?: ReactNode;
  extra?: ReactNode;
}

interface Props {
  label: string;
  choices: Choice[];
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
  /** Columns per row from md (xs: 2). */
  columns?: 3 | 4;
}

const NEXT = new Set(["ArrowRight", "ArrowDown"]);
const PREV = new Set(["ArrowLeft", "ArrowUp"]);

/**
 * A radio group of cards: a single tab stop (the selected card), arrows to
 * move the selection, Enter / Space to select.
 */
function ChoiceGroup({
  label,
  choices,
  value,
  onChange,
  disabled = false,
  columns = 3,
}: Readonly<Props>) {
  const { token } = theme.useToken();
  const refs = useRef<(HTMLDivElement | null)[]>([]);
  const selectedIndex = Math.max(
    0,
    choices.findIndex((c) => c.value === value),
  );

  const onKeyDown = (e: KeyboardEvent, i: number) => {
    if (disabled) return;
    let next = -1;
    if (NEXT.has(e.key)) next = (i + 1) % choices.length;
    else if (PREV.has(e.key)) next = (i - 1 + choices.length) % choices.length;
    else if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      onChange(choices[i].value);
      return;
    }
    if (next >= 0) {
      e.preventDefault();
      onChange(choices[next].value);
      refs.current[next]?.focus();
    }
  };

  return (
    <Row
      gutter={[12, 12]}
      role="radiogroup"
      aria-label={label}
      aria-disabled={disabled}
    >
      {choices.map((c, i) => {
        const selected = c.value === value;
        return (
          <Col key={c.value} xs={12} md={columns === 4 ? 6 : 8}>
            <Card
              ref={(el) => {
                refs.current[i] = el;
              }}
              hoverable={!disabled}
              size="small"
              role="radio"
              aria-checked={selected}
              aria-disabled={disabled}
              tabIndex={i === selectedIndex && !disabled ? 0 : -1}
              onClick={() => !disabled && onChange(c.value)}
              onKeyDown={(e) => onKeyDown(e, i)}
              style={{
                height: "100%",
                cursor: disabled ? "not-allowed" : "pointer",
                opacity: disabled && !selected ? 0.6 : 1,
                borderColor: selected ? token.colorPrimary : undefined,
                background: selected ? token.colorPrimaryBg : undefined,
                boxShadow: selected
                  ? `0 0 0 1px ${token.colorPrimary}`
                  : undefined,
              }}
            >
              <Flex vertical gap={4}>
                <Flex justify="space-between" align="flex-start" gap="small">
                  <Typography.Text strong>{c.title}</Typography.Text>
                  {selected && (
                    <CheckCircleFilled
                      aria-hidden
                      style={{ color: token.colorPrimary }}
                    />
                  )}
                </Flex>
                {c.description && (
                  <Typography.Text
                    type="secondary"
                    style={{ fontSize: token.fontSizeSM }}
                  >
                    {c.description}
                  </Typography.Text>
                )}
                {c.extra}
              </Flex>
            </Card>
          </Col>
        );
      })}
    </Row>
  );
}

export default memo(ChoiceGroup);

import { Col, Form, InputNumber, Row, Slider, Switch } from "antd";
import { HostingFormValues } from "./formValues";

interface Props {
  field: "memoryMB" | "cpus";
  label: string;
  toggleLabel: string;
  help: string;
  unit: string;
  min: number;
  max: number;
  step: number;
  precision: number;
  /** value set when the limit is switched on */
  initialLimit: number;
  requiredMessage: string;
}

/**
 * Optional resource limit: 0 means "no limit" (switch off); switching it on
 * reveals a slider and an input bounded by the server limits.
 */
export default function ResourceLimitField({
  field,
  label,
  toggleLabel,
  help,
  unit,
  min,
  max,
  step,
  precision,
  initialLimit,
  requiredMessage,
}: Readonly<Props>) {
  const form = Form.useFormInstance<HostingFormValues>();
  const name = ["resources", field] as const;
  const value: number | null | undefined = Form.useWatch([...name], form);
  // 0 = no limit. null comes from clearing the input while limited: the
  // inputs stay shown (and the required rule blocks the submission).
  const limited = value !== 0 && value !== undefined;

  const toggle = (on: boolean) =>
    form.setFieldValue(
      [...name],
      on ? Math.min(Math.max(initialLimit, min), max) : 0,
    );

  return (
    <Form.Item label={label} extra={help}>
      <Row gutter={12} align="middle" wrap={false}>
        <Col flex="none">
          <Switch
            checked={limited}
            onChange={toggle}
            aria-label={toggleLabel}
          />
        </Col>
        <Col flex="none">{toggleLabel}</Col>
      </Row>
      {limited && (
        <Row gutter={12} align="middle" style={{ marginTop: 8 }}>
          <Col flex="auto">
            <Form.Item name={[...name]} noStyle>
              <Slider min={min} max={max} step={step} aria-label={label} />
            </Form.Item>
          </Col>
          <Col>
            <Form.Item
              name={[...name]}
              noStyle
              rules={[{ required: true, message: requiredMessage }]}
            >
              <InputNumber
                min={min}
                max={max}
                step={step}
                precision={precision}
                suffix={unit}
                style={{ width: 130 }}
                aria-label={label}
              />
            </Form.Item>
          </Col>
        </Row>
      )}
      {/* Keeps the value (0 = unlimited) registered while the inputs are hidden. */}
      {!limited && <Form.Item name={[...name]} noStyle hidden />}
    </Form.Item>
  );
}

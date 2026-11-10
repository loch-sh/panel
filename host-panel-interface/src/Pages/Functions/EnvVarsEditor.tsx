import { DeleteOutlined, PlusOutlined } from "@ant-design/icons";
import { Button, Flex, Form, Input, Typography } from "antd";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import {
  ENV_NAME_PATTERN,
  envValueProblem,
  EnvVarModel,
  isReservedEnvName,
  MAX_ENV_ENTRIES,
} from "../../Services/Functions/Models/functions.model";

/** Why a row is refused (same rules as the API, which stays the judge). */
export function envRowProblem(
  t: TFunction<"functions">,
  row: EnvVarModel,
  index: number,
  rows: readonly EnvVarModel[],
  otherNames: readonly string[] = [],
): { name?: string; value?: string } {
  const out: { name?: string; value?: string } = {};
  if (!row.name) out.name = t("env.nameRequired");
  else if (!ENV_NAME_PATTERN.test(row.name)) out.name = t("env.nameInvalid");
  else if (isReservedEnvName(row.name)) out.name = t("env.nameReserved");
  else if (
    rows.some((r, i) => i !== index && r.name === row.name) ||
    otherNames.includes(row.name)
  ) {
    out.name = t("env.nameDuplicate");
  }
  const value = envValueProblem(row.value);
  if (value) out.value = t(`env.value.${value}`);
  return out;
}

/** Whether every row is acceptable. */
export function envRowsValid(
  t: TFunction<"functions">,
  rows: readonly EnvVarModel[],
  otherNames: readonly string[] = [],
): boolean {
  return rows.every((r, i) => {
    const p = envRowProblem(t, r, i, rows, otherNames);
    return !p.name && !p.value;
  });
}

interface Props {
  value: EnvVarModel[];
  onChange: (value: EnvVarModel[]) => void;
  /** values typed once and never shown again (password inputs) */
  secret?: boolean;
  /** names used elsewhere (variables vs secrets) */
  otherNames?: readonly string[];
  disabled?: boolean;
  /** shows the problems (after a first submit) */
  showErrors?: boolean;
  /** variables + secrets already used against the limit of 100 */
  used?: number;
}

/** Name / value rows of variables or secrets. */
export default function EnvVarsEditor({
  value,
  onChange,
  secret = false,
  otherNames = [],
  disabled = false,
  showErrors = false,
  used = 0,
}: Readonly<Props>) {
  const { t } = useTranslation("functions");
  const kind = secret ? "secret" : "variable";
  const set = (i: number, patch: Partial<EnvVarModel>) =>
    onChange(value.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const full = value.length + used >= MAX_ENV_ENTRIES;

  return (
    <Flex vertical gap="small">
      {value.length === 0 && (
        <Typography.Text type="secondary">
          {t(`env.${kind}.empty`)}
        </Typography.Text>
      )}
      {value.map((row, i) => {
        const problem = envRowProblem(t, row, i, value, otherNames);
        // Only once something was typed, or after a submit.
        const nameError = (showErrors || row.name !== "") && problem.name;
        const valueError = problem.value;
        return (
          <Flex key={i} gap="small" align="flex-start" wrap>
            <Form.Item
              validateStatus={nameError ? "error" : undefined}
              help={nameError || undefined}
              style={{ flex: "1 1 200px", marginBottom: 0 }}
            >
              <Input
                aria-label={t(`env.${kind}.nameLabel`, { index: i + 1 })}
                placeholder={t(`env.${kind}.namePlaceholder`)}
                value={row.name}
                disabled={disabled}
                autoComplete="off"
                spellCheck={false}
                style={{ fontFamily: "monospace" }}
                onChange={(e) => set(i, { name: e.target.value.trim() })}
              />
            </Form.Item>
            <Form.Item
              validateStatus={valueError ? "error" : undefined}
              help={valueError || undefined}
              style={{ flex: "2 1 260px", marginBottom: 0 }}
            >
              {secret ? (
                <Input.Password
                  aria-label={t(`env.${kind}.valueLabel`, { index: i + 1 })}
                  placeholder={t(`env.${kind}.valuePlaceholder`)}
                  value={row.value}
                  disabled={disabled}
                  autoComplete="new-password"
                  visibilityToggle={false}
                  onChange={(e) => set(i, { value: e.target.value })}
                />
              ) : (
                <Input
                  aria-label={t(`env.${kind}.valueLabel`, { index: i + 1 })}
                  placeholder={t(`env.${kind}.valuePlaceholder`)}
                  value={row.value}
                  disabled={disabled}
                  autoComplete="off"
                  spellCheck={false}
                  style={{ fontFamily: "monospace" }}
                  onChange={(e) => set(i, { value: e.target.value })}
                />
              )}
            </Form.Item>
            <Button
              aria-label={t(`env.${kind}.remove`, { index: i + 1 })}
              icon={<DeleteOutlined />}
              disabled={disabled}
              onClick={() => onChange(value.filter((_, j) => j !== i))}
            />
          </Flex>
        );
      })}
      <Button
        icon={<PlusOutlined />}
        disabled={disabled || full}
        onClick={() => onChange([...value, { name: "", value: "" }])}
        style={{ alignSelf: "flex-start" }}
      >
        {t(`env.${kind}.add`)}
      </Button>
    </Flex>
  );
}

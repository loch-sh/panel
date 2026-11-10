import { Form, Input } from "antd";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { hostingService } from "../../../Services/Hosting/hosting.service";
import {
  CheckNameResponse,
  HOSTING_NAME_MAX_LENGTH,
  HOSTING_NAME_PATTERN,
} from "../../../Services/Hosting/Models/hosting.model";

/** Delay before the name is validated (and checked by the API) while typing. */
const CHECK_DEBOUNCE_MS = 400;

/** Translated reason of an unavailable name (GET /hosting/stacks/check-name). */
function takenMessage(t: TFunction<"hosting">, res: CheckNameResponse): string {
  if (res.code === "INVALID_HOSTING_NAME") {
    return t("form.fields.name.invalid");
  }
  switch (res.reason) {
    case "reserved":
    case "stack":
    case "directory":
    case "compose_project":
    case "container":
    case "traefik_router":
      return t(`form.fields.name.taken.${res.reason}`);
    default:
      return t("form.fields.name.taken.unknown");
  }
}

/**
 * Stack name: validated locally, then checked for availability by the API
 * (debounced). Read-only in edit mode.
 */
export default function NameField({ isEdit }: Readonly<{ isEdit: boolean }>) {
  const { t } = useTranslation("hosting");
  return (
    <Form.Item
      name="name"
      label={t("form.fields.name.label")}
      extra={
        isEdit
          ? undefined
          : t("form.fields.name.help", { max: HOSTING_NAME_MAX_LENGTH })
      }
      validateDebounce={isEdit ? undefined : CHECK_DEBOUNCE_MS}
      // Revalidated when the site choice changes (it changes what is checked).
      dependencies={isEdit ? undefined : ["siteMode"]}
      hasFeedback={!isEdit}
      rules={
        isEdit
          ? []
          : [
              { required: true, message: t("form.fields.name.required") },
              {
                max: HOSTING_NAME_MAX_LENGTH,
                message: t("form.fields.name.tooLong", {
                  max: HOSTING_NAME_MAX_LENGTH,
                }),
              },
              {
                pattern: HOSTING_NAME_PATTERN,
                message: t("form.fields.name.invalid"),
              },
              {
                validator: async (_, value?: string) => {
                  const candidate = (value ?? "").trim();
                  if (
                    !candidate ||
                    candidate.length > HOSTING_NAME_MAX_LENGTH ||
                    !HOSTING_NAME_PATTERN.test(candidate)
                  ) {
                    return;
                  }
                  let res: CheckNameResponse;
                  try {
                    res = await hostingService.checkName(candidate);
                  } catch {
                    throw new Error(t("form.fields.name.checkFailed"));
                  }
                  if (!res.available) throw new Error(takenMessage(t, res));
                },
              },
            ]
      }
    >
      <Input
        disabled={isEdit}
        placeholder={t("form.fields.name.placeholder")}
        maxLength={HOSTING_NAME_MAX_LENGTH}
        autoComplete="off"
        spellCheck={false}
      />
    </Form.Item>
  );
}

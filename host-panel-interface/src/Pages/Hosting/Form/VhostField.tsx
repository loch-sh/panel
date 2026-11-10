import { Form, Input, Typography } from "antd";
import { useTranslation } from "react-i18next";
import ByteCounter from "./ByteCounter";
import { HostingFormValues } from "./formValues";
import { MONOSPACE, snippetSizeRule } from "./snippetRules";

/**
 * Examples of vhost directives (configuration syntax, not translated): cache
 * headers for static files.
 */
const VHOST_EXAMPLE_NGINX = `location ~* \\.(css|js|png|jpg|svg|woff2)$ {
    expires 30d;
    add_header Cache-Control "public";
}`;
const VHOST_EXAMPLE_APACHE = `<FilesMatch "\\.(css|js|png|jpg|svg|woff2)$">
    Header set Cache-Control "max-age=2592000, public"
</FilesMatch>`;

interface Props {
  usesNginx: boolean;
  maxBytes: number;
}

/** Extra directives inserted inside the server / VirtualHost block. */
export default function VhostField({ usesNginx, maxBytes }: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  const form = Form.useFormInstance<HostingFormValues>();
  const vhost = Form.useWatch("vhost", form);
  const sizeRule = snippetSizeRule(t, maxBytes);

  return (
    <Form.Item
      label={t("form.fields.vhost.label")}
      htmlFor="vhost"
      tooltip={
        usesNginx
          ? t("form.fields.vhost.tooltipNginx")
          : t("form.fields.vhost.tooltipApache")
      }
      extra={
        <>
          <Typography.Paragraph type="secondary" style={{ margin: "4px 0" }}>
            {usesNginx
              ? t("form.fields.vhost.helpNginx")
              : t("form.fields.vhost.helpApache")}
          </Typography.Paragraph>
          <pre style={{ ...MONOSPACE, margin: "0 0 4px" }}>
            {usesNginx ? VHOST_EXAMPLE_NGINX : VHOST_EXAMPLE_APACHE}
          </pre>
        </>
      }
    >
      <Form.Item name="vhost" noStyle rules={[sizeRule]}>
        <Input.TextArea
          rows={5}
          placeholder={
            usesNginx
              ? t("form.fields.vhost.placeholderNginx")
              : t("form.fields.vhost.placeholderApache")
          }
          spellCheck={false}
          autoComplete="off"
          style={MONOSPACE}
        />
      </Form.Item>
      <ByteCounter text={vhost} max={maxBytes} />
    </Form.Item>
  );
}

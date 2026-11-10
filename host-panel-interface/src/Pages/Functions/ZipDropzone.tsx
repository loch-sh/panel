import { InboxOutlined } from "@ant-design/icons";
import { Typography, Upload } from "antd";
import { useTranslation } from "react-i18next";
import { toast } from "react-toastify";
import { formatBytes } from "../../Utils/bytes";

interface Props {
  maxBytes: number;
  disabled?: boolean;
  onFile: (file: File) => void;
}

/** Drop zone for one zip, checked for its type and size before anything is sent. */
export default function ZipDropzone({
  maxBytes,
  disabled = false,
  onFile,
}: Readonly<Props>) {
  const { t } = useTranslation("functions");
  return (
    <Upload.Dragger
      accept=".zip,application/zip"
      multiple={false}
      showUploadList={false}
      disabled={disabled}
      // Never uploaded by antd: the caller sends the file itself.
      beforeUpload={(file) => {
        if (!/\.zip$/i.test(file.name)) {
          toast.error(t("code.notZip"));
        } else if (file.size > maxBytes) {
          toast.error(t("code.tooLarge", { max: formatBytes(maxBytes) }));
        } else {
          onFile(file);
        }
        return Upload.LIST_IGNORE;
      }}
    >
      <p className="ant-upload-drag-icon">
        <InboxOutlined />
      </p>
      <Typography.Paragraph style={{ marginBottom: 4 }}>
        {t("code.drop")}
      </Typography.Paragraph>
      <Typography.Text type="secondary">
        {t("code.dropHint", { max: formatBytes(maxBytes) })}
      </Typography.Text>
    </Upload.Dragger>
  );
}

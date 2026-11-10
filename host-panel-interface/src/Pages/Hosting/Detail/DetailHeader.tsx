import { ArrowLeftOutlined, DeleteOutlined } from "@ant-design/icons";
import { Button, Flex, Tooltip } from "antd";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import { PanelTitle } from "../../../Components/Base/PanelTitle";
import { HostingStackDetail } from "../../../Services/Hosting/Models/hosting.model";
import HostingStackActions from "../HostingStackActions";
import HostingStatus from "../HostingStatus";

interface Props {
  stack: HostingStackDetail;
  onDelete: () => void;
}

/** Back button, name, status and actions of the hosting detail page. */
export default function DetailHeader({ stack, onDelete }: Readonly<Props>) {
  const { t } = useTranslation("hosting");
  const navigate = useNavigate();
  const busy = stack.operation?.state === "running";

  return (
    <Flex justify="space-between" align="center" wrap gap="middle">
      <Flex gap="small" align="center" wrap>
        <Button
          type="text"
          icon={<ArrowLeftOutlined />}
          aria-label={t("backToList")}
          onClick={() => navigate("/hosting")}
        />
        <PanelTitle>{stack.name}</PanelTitle>
        <HostingStatus {...stack} />
      </Flex>
      <Flex gap="small" align="center">
        <HostingStackActions stack={stack} />
        <Tooltip title={t("actions.delete")}>
          <Button
            danger
            icon={<DeleteOutlined />}
            aria-label={t("actions.delete")}
            disabled={busy}
            onClick={onDelete}
          />
        </Tooltip>
      </Flex>
    </Flex>
  );
}

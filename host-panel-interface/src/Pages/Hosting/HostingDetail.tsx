import { LoadingOutlined } from "@ant-design/icons";
import { Alert, Button, Flex, Result, Skeleton, Tabs } from "antd";
import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useParams } from "react-router";
import { toast } from "react-toastify";
import useHostingConfig from "../../Services/Hosting/Hooks/useHostingConfig";
import useHostingDelete from "../../Services/Hosting/Hooks/useHostingDelete";
import useHostingStack, {
  isStackNotFound,
} from "../../Services/Hosting/Hooks/useHostingStack";
import { getApiErrorMessage } from "../../Utils/apiErrors";
import DeleteStackModal from "./DeleteStackModal";
import DetailHeader from "./Detail/DetailHeader";
import useDetailTabs, { DetailTabKey } from "./Detail/useDetailTabs";
import useStackOperationEvents from "./Detail/useStackOperationEvents";
import HostingDisabled from "./HostingDisabled";

/** Detail page of a hosted stack (/hosting/:name). */
export default function HostingDetail() {
  const { t } = useTranslation("hosting");
  const navigate = useNavigate();
  const { name = "" } = useParams<{ name: string }>();
  const [tab, setTab] = useState<DetailTabKey>("overview");
  const [deleteOpen, setDeleteOpen] = useState(false);

  const {
    data: config,
    isLoading: loadingConfig,
    error: configError,
    refetch: refetchConfig,
  } = useHostingConfig();
  const enabled = config?.enabled ?? false;
  const {
    data: stack,
    error,
    isLoading,
    isFetching,
  } = useHostingStack(enabled ? name : undefined);
  const { mutate: remove, isPending: deleting } = useHostingDelete();

  // A 404 is final only once no request is in flight.
  const notFound = isStackNotFound(error) && !isFetching;
  const showOperation = useCallback(() => setTab("operation"), []);
  const { deleted, track } = useStackOperationEvents(
    name,
    stack,
    notFound,
    showOperation,
  );
  const tabItems = useDetailTabs(stack, config, track);

  const handleDelete = useCallback(
    (purgeData: boolean) =>
      remove(
        { name, purgeData },
        {
          onSuccess: () => {
            toast.info(t("toasts.deleteStarted", { name }));
            setDeleteOpen(false);
          },
          onError: (err) => toast.error(getApiErrorMessage(err)),
        },
      ),
    [remove, name, t],
  );

  if (loadingConfig || isLoading) return <Skeleton />;
  if (!config || !enabled) {
    return (
      <HostingDisabled
        error={configError}
        onRetry={() => void refetchConfig()}
      />
    );
  }
  if (deleted) return <Skeleton />;
  if (!stack || notFound) {
    return (
      <Result
        status="404"
        title={t("detail.notFound")}
        extra={
          <Button onClick={() => navigate("/hosting")}>
            {t("backToList")}
          </Button>
        }
      />
    );
  }

  const operation = stack.operation;
  const isDeleting =
    operation?.state === "running" && operation.kind === "delete";
  const sitePath = config.sitesDirectory
    ? `${config.sitesDirectory}/${stack.site}`
    : stack.site;

  return (
    <Flex vertical gap="middle" style={{ maxWidth: 1100 }}>
      <DetailHeader stack={stack} onDelete={() => setDeleteOpen(true)} />

      {stack.secretsMasked && (
        <Alert type="info" showIcon title={t("detail.secretsMasked")} />
      )}

      {isDeleting && (
        <Alert
          type="warning"
          showIcon
          icon={<LoadingOutlined />}
          title={t("detail.deleting")}
        />
      )}

      <Tabs
        activeKey={tab}
        onChange={(key) => setTab(key as DetailTabKey)}
        items={tabItems}
      />

      <DeleteStackModal
        open={deleteOpen}
        name={stack.name}
        site={stack.site}
        sitePath={sitePath}
        loading={deleting}
        onConfirm={handleDelete}
        onClose={() => setDeleteOpen(false)}
      />
    </Flex>
  );
}

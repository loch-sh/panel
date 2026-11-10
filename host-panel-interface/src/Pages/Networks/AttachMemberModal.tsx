import { Alert, Empty, Flex, Form, Modal, Select, Typography } from "antd";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { ServiceInstanceModel } from "../../Services/Databases/Models/databases.model";
import { FunctionSummaryModel } from "../../Services/Functions/Models/functions.model";
import { HostingStackSummary } from "../../Services/Hosting/Models/hosting.model";
import {
  AttachableKind,
  NetworkDetailModel,
} from "../../Services/Networks/Models/networks.model";
import {
  AttachTarget,
  decodeMember,
  encodeMember,
  memberOptions,
} from "./memberOptions";

export type { AttachTarget } from "./memberOptions";

const { Text } = Typography;

interface Props {
  open: boolean;
  network: NetworkDetailModel;
  stacks: readonly HostingStackSummary[];
  instances: readonly ServiceInstanceModel[];
  functions?: readonly FunctionSummaryModel[];
  loading: boolean;
  onCancel: () => void;
  onSubmit: (target: AttachTarget) => void;
}

/**
 * Chooses a site or a service instance to attach to a network, and says
 * which container restarts.
 */
export default function AttachMemberModal({
  open,
  network,
  stacks,
  instances,
  functions,
  loading,
  onCancel,
  onSubmit,
}: Readonly<Props>) {
  const { t } = useTranslation("networks");
  const { t: tdb } = useTranslation("databases");
  const [value, setValue] = useState<string>();

  // Members already declared on the network are not offered again.
  const declared = useMemo(
    () =>
      new Set(
        network.members
          .filter((m) => m.expected)
          .map((m) => encodeMember(m.kind as AttachableKind, m.name)),
      ),
    [network.members],
  );

  const options = useMemo(
    () =>
      memberOptions(
        t,
        tdb,
        stacks,
        instances,
        { system: !!network.system, declared },
        functions,
      ),
    [stacks, instances, functions, declared, network.system, t, tdb],
  );

  const target = value ? decodeMember(value) : undefined;
  const instance =
    target?.kind === "instance"
      ? instances.find((i) => i.key === target.name)
      : undefined;

  return (
    <Modal
      open={open}
      title={t("attach.title", { network: network.name })}
      okText={t("attach.submit")}
      cancelText={t("actions.cancel", { ns: "common" })}
      okButtonProps={{ disabled: !target, loading }}
      onCancel={onCancel}
      // Reset once closed, so a failed attach keeps the choice for a retry.
      afterClose={() => setValue(undefined)}
      onOk={() => {
        if (target) onSubmit(target);
      }}
      destroyOnHidden
    >
      <Flex vertical gap="middle">
        <Text type="secondary">{t("attach.intro")}</Text>
        {options.length === 0 ? (
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description={t("attach.noResource")}
          />
        ) : (
          <Form layout="vertical">
            <Form.Item
              label={t("attach.resource")}
              htmlFor="attach-resource"
              style={{ marginBottom: 0 }}
            >
              <Select
                id="attach-resource"
                showSearch={{ optionFilterProp: "title" }}
                value={value}
                onChange={setValue}
                options={options}
                placeholder={t("attach.placeholder")}
                optionLabelProp="title"
              />
            </Form.Item>
          </Form>
        )}
        {target && (
          <Alert
            type="warning"
            showIcon
            message={
              target.kind === "site"
                ? t("attach.restart.site", { name: target.name })
                : target.kind === "function"
                  ? t("attach.restart.function", { name: target.name })
                  : t("attach.restart.instance", {
                      container: instance?.project ?? target.name,
                    })
            }
          />
        )}
      </Flex>
    </Modal>
  );
}

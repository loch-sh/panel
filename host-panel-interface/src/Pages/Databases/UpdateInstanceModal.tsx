import { Alert, Flex, Modal, Radio, Typography } from "antd";
import { isAxiosError } from "axios";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "react-toastify";
import { useInstanceUpgrade } from "../../Services/Databases/Hooks/useDatabaseMutations";
import {
  compareVersions,
  findVersion,
  ServiceEngineModel,
  ServiceInstanceModel,
  ServiceVersionModel,
} from "../../Services/Databases/Models/databases.model";
import { getApiErrorMessage } from "../../Utils/apiErrors";
import ServiceSupportTags from "./ServiceSupportTags";

const { Text } = Typography;

/** Choice of the latest image of the current version (no version sent). */
const IMAGE_CHOICE = "";

interface Props {
  instance: ServiceInstanceModel | null;
  engine: ServiceEngineModel | undefined;
  engineName: string;
  onClose: () => void;
}

/** The versions an instance can move to: strictly newer only (no downgrade). */
export function newerVersions(i: ServiceInstanceModel): ServiceVersionModel[] {
  return (i.update?.versions ?? []).filter(
    (v) => compareVersions(v.version, i.version) > 0,
  );
}

/** The API error code of a failed request, if any. */
function errorCode(err: unknown): string | undefined {
  if (!isAxiosError(err)) return undefined;
  const data: unknown = err.response?.data;
  return data && typeof data === "object" && "code" in data
    ? String(data.code)
    : undefined;
}

/**
 * Updates a dedicated instance: to the latest image of its version, or to a
 * newer version (never back). The instance restarts.
 */
export default function UpdateInstanceModal({
  instance,
  engine,
  engineName,
  onClose,
}: Readonly<Props>) {
  const { t } = useTranslation("databases");
  const { mutateAsync: upgrade, isPending } = useInstanceUpgrade();
  const [picked, setPicked] = useState<string | null>(null);
  const name = instance?.name ?? instance?.project ?? "";
  const versions = useMemo(
    () => (instance ? newerVersions(instance) : []),
    [instance],
  );
  const imageAvailable = !!instance?.update?.image;
  const hasUpdate = imageAvailable || versions.length > 0;
  // The latest image of the current version is preselected (no data format
  // change). A version change is irreversible and done without a backup:
  // it is never preselected, it must be picked explicitly.
  const choice = picked ?? (imageAvailable ? IMAGE_CHOICE : null);
  const newVersion = choice ? versions.find((v) => v.version === choice) : null;
  const current = instance
    ? {
        supportUntil: instance.supportUntil,
        activeSupportUntil: instance.activeSupportUntil,
        deprecated: instance.deprecated,
        pinned: findVersion(engine, instance.version)?.pinned,
      }
    : {};

  const close = () => {
    setPicked(null);
    onClose();
  };
  const submit = async () => {
    if (!instance || choice === null) return;
    try {
      const res = await upgrade({
        instance: instance.key,
        version: choice || undefined,
      });
      if (res.updated) {
        toast.success(
          t("update.updated", {
            name,
            engine: engineName,
            version: res.version,
          }),
        );
      } else {
        toast.info(t("update.upToDate", { name }));
      }
      close();
    } catch (err) {
      // The update failed to start: the previous version runs again.
      toast.error(
        errorCode(err) === "SERVICE_INSTANCE_START_FAILED"
          ? t("update.startFailed")
          : getApiErrorMessage(err),
      );
    }
  };

  return (
    <Modal
      open={instance !== null}
      title={t("update.title", { name })}
      okText={t("update.submit")}
      okButtonProps={{ disabled: choice === null }}
      confirmLoading={isPending}
      cancelText={t("actions.cancel", { ns: "common" })}
      cancelButtonProps={{ disabled: isPending }}
      closable={!isPending}
      maskClosable={!isPending}
      onOk={() => void submit()}
      onCancel={close}
      destroyOnHidden
    >
      {instance && (
        <Flex vertical gap="middle">
          <Flex vertical gap={4}>
            <Text type="secondary">{t("update.current")}</Text>
            <Flex gap="small" align="center" wrap>
              <Text strong>{`${engineName} ${instance.version}`}</Text>
              <ServiceSupportTags support={current} />
            </Flex>
            <Text code style={{ wordBreak: "break-all" }}>
              {instance.image}
            </Text>
          </Flex>

          {!hasUpdate ? (
            <Alert type="success" showIcon message={t("update.none")} />
          ) : (
            <Flex vertical gap="small">
              <Text strong id="update-choice-label">
                {t("update.choose")}
              </Text>
              <Radio.Group
                aria-labelledby="update-choice-label"
                value={choice}
                disabled={isPending}
                onChange={(e) => setPicked(e.target.value as string)}
              >
                <Flex vertical gap="small">
                  {imageAvailable && (
                    <Radio value={IMAGE_CHOICE}>
                      <Flex vertical gap={2}>
                        <Text>
                          {t("update.image", {
                            engine: engineName,
                            version: instance.version,
                          })}
                        </Text>
                        {instance.update?.latestImage && (
                          <Text
                            code
                            type="secondary"
                            style={{ wordBreak: "break-all" }}
                          >
                            {instance.update.latestImage}
                          </Text>
                        )}
                        <Text type="secondary">{t("update.imageHint")}</Text>
                      </Flex>
                    </Radio>
                  )}
                  {versions.map((v) => (
                    <Radio key={v.version} value={v.version}>
                      <Flex vertical gap={2}>
                        <Text>{`${engineName} ${v.version}`}</Text>
                        <ServiceSupportTags support={v} />
                      </Flex>
                    </Radio>
                  ))}
                </Flex>
              </Radio.Group>
            </Flex>
          )}

          <Alert
            type="warning"
            showIcon
            message={t("update.restartWarning")}
            description={t("update.rollbackNote")}
          />
          {newVersion && (
            <Alert
              type="error"
              showIcon
              message={t("update.noDowngrade", {
                engine: engineName,
                version: newVersion.version,
              })}
            />
          )}
        </Flex>
      )}
    </Modal>
  );
}

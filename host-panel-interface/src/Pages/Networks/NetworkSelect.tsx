import { Flex, Select, Tag, Typography } from "antd";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { NetworkSummaryModel } from "../../Services/Networks/Models/networks.model";
import { NETWORKS_PATH } from "./networkPaths";

export interface NetworkSelectProps {
  id?: string;
  value?: string[];
  onChange?: (value: string[]) => void;
  networks: readonly NetworkSummaryModel[] | undefined;
  loading?: boolean;
  disabled?: boolean;
  /** networks that cannot be chosen, with the reason */
  disabledReasons?: Record<string, string>;
  /** offers a link to the creation page below the select */
  showCreateLink?: boolean;
  "aria-label"?: string;
  status?: "error" | "warning";
}

/**
 * Multiple choice of private networks. A network not in the list (deleted,
 * or not readable) stays shown when selected, so it can be removed.
 */
export default function NetworkSelect({
  id,
  value,
  onChange,
  networks,
  loading,
  disabled,
  disabledReasons,
  showCreateLink = false,
  status,
  "aria-label": ariaLabel,
}: Readonly<NetworkSelectProps>) {
  const { t } = useTranslation("networks");
  const options = useMemo(() => {
    const known = new Set((networks ?? []).map((n) => n.name));
    const extra = (value ?? []).filter((v) => !known.has(v));
    return [
      ...(networks ?? []).map((n) => {
        const reason =
          disabledReasons?.[n.name] ??
          (n.state === "ok" ? undefined : t(`state.${n.state}`));
        const selected = value?.includes(n.name) ?? false;
        return {
          value: n.name,
          // A selected network stays removable even when unavailable.
          disabled: !!reason && !selected,
          label: (
            <Flex gap={6} align="center" wrap>
              <span>{n.name}</span>
              {n.system && <Tag color="geekblue">{t("tags.shared")}</Tag>}
              {n.subnet && (
                <Typography.Text type="secondary">{n.subnet}</Typography.Text>
              )}
              {reason && (
                <Typography.Text type="secondary">({reason})</Typography.Text>
              )}
            </Flex>
          ),
          title: n.name,
        };
      }),
      ...extra.map((v) => ({
        value: v,
        label: (
          <Flex gap={6} align="center">
            <span>{v}</span>
            <Typography.Text type="secondary">
              ({t("select.unavailable")})
            </Typography.Text>
          </Flex>
        ),
        title: v,
      })),
    ];
  }, [networks, value, disabledReasons, t]);

  return (
    <Flex vertical gap={4}>
      <Select
        id={id}
        mode="multiple"
        aria-label={ariaLabel}
        value={value}
        onChange={onChange}
        options={options}
        loading={loading}
        disabled={disabled}
        status={status}
        placeholder={t("select.placeholder")}
        optionLabelProp="title"
        style={{ width: "100%" }}
      />
      {showCreateLink && (
        <Link to={`${NETWORKS_PATH}/new`}>{t("select.createLink")}</Link>
      )}
    </Flex>
  );
}

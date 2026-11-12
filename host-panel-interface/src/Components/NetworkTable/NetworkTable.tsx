import Table, { ColumnsType } from "antd/es/table";
import type { TFunction } from "i18next";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

type PortsType = {
  HostIp: string;
  HostPort: string;
};

interface Props {
  /** Docker's NetworkSettings.Ports (a port without binding maps to null). */
  ports: Record<string, PortsType[] | null> | null;
}

interface MappedType {
  Protocol: string;
  LocalPort: string;
  HostIp: string;
  HostPort: string;
}

export default function NetworkTable({ ports }: Readonly<Props>) {
  const { t } = useTranslation("containers");
  const columns = useMemo(() => getColumns(t), [t]);
  const mapped: MappedType[] = useMemo(() => {
    return Object.entries(ports ?? {}).flatMap(([key, value]) => {
        const [localPort, protocol] = key.split("/");

        return (value ?? []).map((el) => ({
          Protocol: protocol,
          LocalPort: localPort,
          HostIp: el.HostIp,
          HostPort: el.HostPort,
        }));
      });
  }, [ports]);

  return mapped.length === 0 ? (
    <p>{t("sidePanel.none")}</p>
  ) : (
    <Table
      columns={columns}
      dataSource={mapped}
      rowKey={(r) => `${r.HostIp}:${r.HostPort}/${r.Protocol}`}
      size="small"
      pagination={mapped.length > 10 ? undefined : false}
    />
  );
}

const getColumns = (t: TFunction<"containers">): ColumnsType<MappedType> => [
  {
    title: t("networkTable.ip"),
    dataIndex: "HostIp",
  },
  {
    title: t("networkTable.protocol"),
    dataIndex: "Protocol",
    render: (value: string) => value?.toLocaleUpperCase(),
  },
  {
    title: t("networkTable.localPort"),
    dataIndex: "LocalPort",
  },
  {
    title: t("networkTable.hostPort"),
    dataIndex: "HostPort",
  },
];

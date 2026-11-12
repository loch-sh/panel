import { Flex, Table } from "antd";
import { ColumnsType } from "antd/es/table";
import type { TFunction } from "i18next";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

interface Mount {
  Type: "bind" | "volume" | "image" | "tmpfs" | "npipe" | "cluster";
  Name?: string;
  Source: string;
  Destination: string;
  Driver?: string;
  Mode: string;
  RW: boolean;
  Propagation: string;
}

const getColumns = (t: TFunction<"containers">): ColumnsType<Mount> => [
  {
    title: t("mountTable.type"),
    dataIndex: "Type",
  },
  {
    title: t("mountTable.source"),
    dataIndex: "Source",
    render: (value) => (
      <Flex wrap style={{ width: 180 }}>
        <span style={{ wordBreak: "break-all" }}>{value}</span>
      </Flex>
    ),
  },
  {
    title: t("mountTable.destination"),
    dataIndex: "Destination",
  },
];

export default function MountTable({ item }: Readonly<{ item: Mount[] }>) {
  const { t } = useTranslation("containers");
  const columns = useMemo(() => getColumns(t), [t]);
  return (
    <Table
      columns={columns}
      dataSource={item}
      rowKey="Destination"
      size="small"
      pagination={item.length > 10 ? undefined : false}
      scroll={{ x: "max-content" }}
    />
  );
}

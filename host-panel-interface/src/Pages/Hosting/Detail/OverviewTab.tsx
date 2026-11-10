import { Flex } from "antd";
import { memo } from "react";
import { HostingStackDetail } from "../../../Services/Hosting/Models/hosting.model";
import ContainersCard from "./Overview/ContainersCard";
import OverviewDescriptions from "./Overview/OverviewDescriptions";
import UsageCard from "./Overview/UsageCard";

interface Props {
  stack: HostingStackDetail;
  sitesDirectory?: string;
  phpDeprecated: boolean;
}

/** Overview tab: settings, live usage and containers of the stack. */
function OverviewTab({
  stack,
  sitesDirectory,
  phpDeprecated,
}: Readonly<Props>) {
  return (
    <Flex vertical gap="large">
      <OverviewDescriptions
        stack={stack}
        sitesDirectory={sitesDirectory}
        phpDeprecated={phpDeprecated}
      />
      <UsageCard containers={stack.containers} />
      <ContainersCard stack={stack} />
    </Flex>
  );
}

export default memo(OverviewTab);

import { Divider } from "antd";
import DomainsFields from "../DomainsFields";
import IdentityFields from "../IdentityFields";
import NetworksField from "../NetworksField";
import { SectionProps } from "./sectionProps";

/** Name, site directory, document root and domains. */
export default function SiteSection({
  mode,
  config,
  sites,
  limits,
}: Readonly<SectionProps>) {
  return (
    <>
      <IdentityFields
        isEdit={mode === "edit"}
        sitesDirectory={config.sitesDirectory}
        sites={sites}
      />
      <Divider style={{ marginTop: 0 }} />
      <DomainsFields maxDomains={limits.maxDomains} />
      <NetworksField />
    </>
  );
}

import { HostingSiteModel } from "../../../Services/Hosting/Models/hosting.model";
import DocumentRootField from "./DocumentRootField";
import NameField from "./NameField";
import SiteField from "./SiteField";

interface Props {
  isEdit: boolean;
  sitesDirectory?: string;
  sites: HostingSiteModel[];
}

/** Identity of the stack: name, site directory and document root. */
export default function IdentityFields({
  isEdit,
  sitesDirectory,
  sites,
}: Readonly<Props>) {
  return (
    <>
      <NameField isEdit={isEdit} />
      <SiteField
        isEdit={isEdit}
        sitesDirectory={sitesDirectory}
        sites={sites}
      />
      <DocumentRootField />
    </>
  );
}

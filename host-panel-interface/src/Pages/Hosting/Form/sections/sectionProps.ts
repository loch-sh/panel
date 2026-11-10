import type { PanelSummaries } from "./usePanelSummaries";
import {
  HostingConfigModel,
  HostingLimits,
  HostingSiteModel,
  HostingTemplate,
} from "../../../../Services/Hosting/Models/hosting.model";

/** Everything the form sections need, computed once by the form. */
export interface SectionProps {
  mode: "create" | "edit";
  config: HostingConfigModel;
  sites: HostingSiteModel[];
  limits: HostingLimits;
  templates: HostingTemplate[];
  /** templates that can be selected (edit: same kind as the current one) */
  allowedTemplates?: HostingTemplate[];
  hasPhp: boolean;
  usesNginx: boolean;
  /** changed when the form is reset: discards the routing section state */
  resetKey?: number;
  onPrefillingChange: (prefilling: boolean) => void;
  /** names of the fields showing an error (opens their panels) */
  errorFields: (string | number)[][];
  /** failed saves / steps so far: reopens the panels in error each time */
  failures: number;
}

/** Props of a section component: the form props plus the panel summaries. */
export interface SectionContentProps extends SectionProps {
  summaries: PanelSummaries;
}

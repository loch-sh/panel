import type { FormInstance } from "antd";
import { ReactNode } from "react";
import { HostingFormValues } from "../formValues";
import { SectionKey } from "../sections/sectionKeys";
import { SectionDef } from "../sections/useSections";

/** Props shared by the creation wizard and the edit layout. */
export interface FormLayoutProps {
  form: FormInstance<HostingFormValues>;
  sections: SectionDef[];
  /** sections holding a field in error */
  errorSections: Set<SectionKey>;
  /** validates and submits; resolves to true on success */
  submit: () => Promise<boolean>;
  submitLabel: string;
  submitting: boolean;
  submitDisabled: boolean;
  extraActions?: ReactNode;
  /** a step or the submission failed (reopens the panels in error) */
  reportFailure: () => void;
}

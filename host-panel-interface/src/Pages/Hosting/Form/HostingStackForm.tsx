import { Form, FormInstance } from "antd";
import { ReactNode, useEffect, useMemo, useState } from "react";
import {
  HostingConfigModel,
  HostingLimits,
  HostingSiteModel,
  HostingTemplate,
  HOSTING_TEMPLATES,
  templateHasPhp,
} from "../../../Services/Hosting/Models/hosting.model";
import EditLayout from "./Edit/EditLayout";
import { deepEqual, sameNames } from "./formState";
import { formValuesToPayload, HostingFormValues } from "./formValues";
import {
  SectionKey,
  fieldsWithErrors,
  sectionOfField,
} from "./sections/sectionKeys";
import useSections from "./sections/useSections";
import CreateWizard from "./Wizard/CreateWizard";

/** Used only if the API does not send its limits (it always should). */
const FALLBACK_LIMITS: HostingLimits = {
  maxMemoryMB: 4096,
  maxCpus: 4,
  maxDomains: 10,
  maxEnvVars: 50,
  maxSnippetBytes: 65536,
};

interface Props {
  form: FormInstance<HostingFormValues>;
  mode: "create" | "edit";
  config: HostingConfigModel;
  sites?: HostingSiteModel[];
  initialValues: HostingFormValues;
  /** templates that can be selected (edit: same kind as the current one) */
  allowedTemplates?: HostingTemplate[];
  submitLabel: string;
  submitting: boolean;
  disabled?: boolean;
  /** resolves to false when the submission failed (errors shown) */
  onSubmit: (values: HostingFormValues) => Promise<boolean | void>;
  /** extra actions next to the submit button */
  extraActions?: ReactNode;
  /** no submit button (settings shown for reference only) */
  readOnly?: boolean;
}

interface FormState {
  errorFields: (string | number)[][];
  dirty: boolean;
}

/**
 * Settings form of a hosted stack: a step wizard in creation, sections with
 * a side navigation in edition. Both share the same sections and fields.
 */
export default function HostingStackForm({
  form,
  mode,
  config,
  sites = [],
  initialValues,
  allowedTemplates,
  submitLabel,
  submitting,
  disabled,
  onSubmit,
  extraActions,
  readOnly,
}: Readonly<Props>) {
  const [prefilling, setPrefilling] = useState(false);
  // Bumped on discard: remounts the routing section (drops its state).
  const [resetKey, setResetKey] = useState(0);

  // Payload the form is compared with to know if there are changes: the
  // initial values, then what was last saved.
  const initialPayload = useMemo(
    () => formValuesToPayload(initialValues),
    [initialValues],
  );
  const [reference, setReference] = useState(initialPayload);
  // Failed saves / steps: reopens the panels holding errors each time.
  const [failures, setFailures] = useState(0);
  const reportFailure = () => setFailures((n) => n + 1);

  // Errors and dirty state, read from the form instance after each change
  // (not during render: the instance is only connected once mounted). The
  // state only changes when they do, not on every keystroke.
  const [state, setState] = useState<FormState>({
    errorFields: [],
    dirty: false,
  });
  const refresh = (ref = reference) => {
    const next: FormState = {
      errorFields: fieldsWithErrors(form),
      dirty: !deepEqual(formValuesToPayload(form.getFieldsValue(true)), ref),
    };
    setState((prev) =>
      prev.dirty === next.dirty && sameNames(prev.errorFields, next.errorFields)
        ? prev
        : next,
    );
  };

  // New server values (refetch after a save, polling): when they really
  // differ and there are no unsaved edits, the form shows them and they
  // become the reference; unsaved edits are kept (with the old reference).
  useEffect(() => {
    if (deepEqual(initialPayload, reference)) return;
    const edited = !deepEqual(
      formValuesToPayload(form.getFieldsValue(true)),
      reference,
    );
    if (edited) return;
    form.resetFields();
    setReference(initialPayload);
    // resetFields does not fire onFieldsChange: clear stale errors here.
    refresh(initialPayload);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only on new server values
  }, [initialPayload]);

  /** Back to the reference values (edit mode "Discard changes"). */
  const discard = () => {
    form.resetFields();
    setResetKey((k) => k + 1);
    // The form now shows the latest server values: they are the reference
    // (a newer server payload may have been kept aside during the edits).
    setReference(initialPayload);
    refresh(initialPayload);
  };

  const template = Form.useWatch("template", form) ?? initialValues.template;
  const { errorFields } = state;
  const errorSections = new Set<SectionKey>(errorFields.map(sectionOfField));

  const sections = useSections(
    {
      mode,
      config,
      sites,
      limits: config.limits ?? FALLBACK_LIMITS,
      templates: config.templates?.length
        ? config.templates
        : [...HOSTING_TEMPLATES],
      allowedTemplates,
      hasPhp: templateHasPhp(template),
      usesNginx: template === "php-nginx" || template === "nginx",
      resetKey,
      onPrefillingChange: setPrefilling,
      errorFields,
      failures,
    },
    form,
  );

  /**
   * Validates every field, then submits the whole store (it also holds the
   * values of unmounted fields, e.g. the site in edit mode). Resolves to
   * true when the submission went through.
   */
  const submit = async (): Promise<boolean> => {
    try {
      await form.validateFields();
    } catch {
      refresh();
      reportFailure();
      return false;
    }
    const values = form.getFieldsValue(true);
    const ok = (await onSubmit(values)) !== false;
    // Saved: the submitted values are the new reference (no unsaved bar).
    const ref = ok ? formValuesToPayload(values) : reference;
    if (ok) setReference(ref);
    else reportFailure();
    refresh(ref);
    return ok;
  };

  const layoutProps = {
    form,
    sections,
    errorSections,
    submit,
    submitLabel,
    submitting,
    // Rules being generated, or an operation running on the stack.
    submitDisabled: prefilling || !!disabled,
    extraActions,
    reportFailure,
  };

  return (
    <Form<HostingFormValues>
      form={form}
      layout="vertical"
      initialValues={initialValues}
      onFieldsChange={() => refresh()}
      disabled={disabled || readOnly}
    >
      {mode === "create" ? (
        <CreateWizard {...layoutProps} sitesDirectory={config.sitesDirectory} />
      ) : (
        <EditLayout
          {...layoutProps}
          readOnly={!!readOnly}
          dirty={state.dirty}
          onDiscard={discard}
        />
      )}
    </Form>
  );
}

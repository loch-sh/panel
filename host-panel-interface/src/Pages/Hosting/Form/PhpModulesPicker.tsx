import { Col, Empty, Flex, Input, Row, Alert } from "antd";
import { memo, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { PhpModuleModel } from "../../../Services/Hosting/Models/hosting.model";
import PhpModuleRow from "./PhpModuleRow";
import { disableModule, enableModule, forcedBy } from "./phpModules";

/** Above this number of modules, a filter input is shown. */
const SEARCH_THRESHOLD = 12;

interface PickerProps {
  /** undefined while the modules of the image are not loaded */
  modules?: PhpModuleModel[];
  value?: string[] | null;
  onChange?: (value: string[]) => void;
  /** id of the visible label, for the group's accessible name */
  labelId: string;
}

/** Grid of switches (on = enabled) controlling the disabled modules list. */
const PhpModulesPicker = memo(function PhpModulesPicker({
  modules,
  value,
  onChange,
  labelId,
}: Readonly<PickerProps>) {
  const { t } = useTranslation("hosting");
  const disabled = useMemo(() => value ?? [], [value]);
  const [search, setSearch] = useState("");
  // Last cascade, for the info message.
  const [cascade, setCascade] = useState<string[]>([]);
  // Names disabled by a cascade (not by the user) during this session.
  const [cascaded, setCascaded] = useState<ReadonlySet<string>>(new Set());

  const visible = useMemo(() => {
    const q = search.trim().toLowerCase();
    const all = modules ?? [];
    return q ? all.filter((m) => m.name.toLowerCase().includes(q)) : all;
  }, [modules, search]);

  if (!modules) return null;

  const toggle = (module: PhpModuleModel, enabled: boolean) => {
    if (!modules) return;
    if (enabled) {
      const next = enableModule(module.name, disabled, modules, cascaded);
      setCascade([]);
      setCascaded((prev) => new Set([...prev].filter((n) => next.includes(n))));
      onChange?.(next);
    } else {
      const res = disableModule(module.name, disabled, modules);
      setCascade(res.added);
      setCascaded((prev) => {
        const set = new Set(prev);
        set.delete(module.name); // now an explicit choice
        res.added.forEach((n) => set.add(n));
        return set;
      });
      onChange?.(res.disabled);
    }
  };

  return (
    <Flex vertical gap="small" role="group" aria-labelledby={labelId}>
      {modules.length > SEARCH_THRESHOLD && (
        <Input.Search
          allowClear
          placeholder={t("form.fields.phpModules.search")}
          aria-label={t("form.fields.phpModules.search")}
          onChange={(e) => setSearch(e.target.value)}
          style={{ maxWidth: 320 }}
        />
      )}
      {cascade.length > 0 && (
        <Alert
          type="info"
          showIcon
          closable={{ onClose: () => setCascade([]) }}
          title={t("form.fields.phpModules.alsoDisabled", {
            modules: cascade.join(", "),
          })}
        />
      )}
      {visible.length === 0 ? (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={t("form.fields.phpModules.empty")}
        />
      ) : (
        <Row gutter={[16, 8]}>
          {visible.map((module) => (
            <Col key={module.name} xs={24} sm={12} lg={8}>
              <PhpModuleRow
                module={module}
                forcedBy={forcedBy(module, disabled)}
                disabled={disabled.includes(module.name)}
                onToggle={(checked) => toggle(module, checked)}
              />
            </Col>
          ))}
        </Row>
      )}
    </Flex>
  );
});

export default PhpModulesPicker;

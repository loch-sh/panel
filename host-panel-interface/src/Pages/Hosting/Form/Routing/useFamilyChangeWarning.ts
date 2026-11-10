import { useState } from "react";
import { ServerFamily } from "../formPaths";

/**
 * Tracks the web server family the custom rules were written for. The rules
 * are NOT regenerated automatically after a family change (they may hold
 * manual edits); the API rejects them while unchanged, so a warning is shown
 * until the user edits (or resets) them.
 */
export default function useFamilyChangeWarning(
  family: ServerFamily,
  initialFamily: ServerFamily,
  rules: string,
) {
  const [rulesFamily, setRulesFamily] = useState(initialFamily);
  // Rules text when the family last changed: editing them clears the warning.
  const [prevFamily, setPrevFamily] = useState(family);
  const [rulesAtChange, setRulesAtChange] = useState<string | null>(null);
  if (family !== prevFamily) {
    setPrevFamily(family);
    setRulesAtChange(rules);
  }

  const showWarning =
    !!rules.trim() && rulesFamily !== family && rules === rulesAtChange;

  return { rulesFamily, setRulesFamily, showWarning };
}

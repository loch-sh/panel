import useFunctions from "./useFunctions";
import useFunctionsConfig from "./useFunctionsConfig";

/**
 * The functions when the module is enabled (admins only: `isAdmin` false
 * calls nothing), e.g. to offer them as network members.
 */
export default function useEnabledFunctions(isAdmin: boolean) {
  const { data: config } = useFunctionsConfig(isAdmin);
  const enabled = isAdmin && !!config?.enabled;
  const { data } = useFunctions(enabled);
  return enabled ? data : undefined;
}

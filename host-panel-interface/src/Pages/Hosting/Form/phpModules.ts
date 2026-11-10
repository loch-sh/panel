import { PhpModuleModel } from "../../../Services/Hosting/Models/hosting.model";

/**
 * The image disables a module X by deleting every ini file whose name
 * contains X, so disabling X also disables every module whose file contains
 * X (e.g. "pgsql" takes "pdo_pgsql" with it). The match is on the full ini
 * file name, like the server. The API requires those modules to be listed
 * too; these helpers keep the list consistent. The list may also hold names
 * absent from the current image (kept as is, they still match files).
 */

/** Disabled module (other than the module itself) whose removal takes it down. */
export function forcedBy(
  module: PhpModuleModel,
  disabled: readonly string[],
): string | undefined {
  return disabled.find((x) => x !== module.name && module.file.includes(x));
}

/** Modules taken down with `name`, besides itself. */
export function dependents(
  name: string,
  modules: readonly PhpModuleModel[],
): PhpModuleModel[] {
  return modules.filter((m) => m.name !== name && m.file.includes(name));
}

/** Disables a module and every module it takes down. */
export function disableModule(
  name: string,
  disabled: readonly string[],
  modules: readonly PhpModuleModel[],
): { disabled: string[]; added: string[] } {
  const added = dependents(name, modules)
    .map((m) => m.name)
    .filter((n) => !disabled.includes(n));
  return { disabled: [...disabled, name, ...added], added };
}

/**
 * Enables a module, and the modules it had taken down (`cascaded`: disabled
 * by a cascade, not by the user) unless another disabled name still takes
 * them down. Modules the user disabled explicitly stay disabled.
 */
export function enableModule(
  name: string,
  disabled: readonly string[],
  modules: readonly PhpModuleModel[],
  cascaded: ReadonlySet<string>,
): string[] {
  let next = disabled.filter((n) => n !== name);
  for (const dep of dependents(name, modules)) {
    if (!cascaded.has(dep.name)) continue;
    const others = next.filter((n) => n !== dep.name);
    if (!forcedBy(dep, others)) next = others;
  }
  return next;
}

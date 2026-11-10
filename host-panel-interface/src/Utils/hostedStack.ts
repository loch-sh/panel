/**
 * Hosted stacks (web hosting feature) are compose projects named
 * "hosted-<name>" on the server. The prefix is an implementation detail that is
 * never shown in the interface: use these helpers for display only, and keep
 * the real project / container name for API calls.
 */
export const HOSTED_STACK_PREFIX = "hosted-";

/** Compose project label set on every container of a compose project. */
export const COMPOSE_PROJECT_LABEL = "com.docker.compose.project";
/** Label set by the API on the containers of the stacks it manages. */
export const MANAGED_LABEL = "sh.loch.managed";

/**
 * True when the compose project name looks like a hosted stack. Only a hint
 * (another project could use the prefix): use it for display, and
 * `isHostedContainer` to tell a real hosted stack apart.
 */
export function isHostedStack(project: string | undefined | null): boolean {
  return (
    !!project &&
    project.startsWith(HOSTED_STACK_PREFIX) &&
    project.length > HOSTED_STACK_PREFIX.length
  );
}

/** Name of a compose project as shown to the user ("hosted-blog" -> "blog"). */
export function displayStackName(project: string): string {
  return isHostedStack(project)
    ? project.slice(HOSTED_STACK_PREFIX.length)
    : project;
}

/**
 * Container, router or service name as shown to the user: strips a leading
 * "hosted-" ("hosted-blog-web-1" -> "blog-web-1"). A leading "/" (Docker API
 * container names) is kept.
 */
export function displayContainerName(name: string): string {
  const slash = name.startsWith("/") ? "/" : "";
  const bare = slash ? name.slice(1) : name;
  return slash + displayStackName(bare);
}

/**
 * True when a container belongs to a stack managed by the hosting feature:
 * project prefixed with "hosted-" and the API's management label.
 */
export function isHostedContainer(
  labels: Record<string, string> | undefined | null,
): boolean {
  return (
    !!labels &&
    isHostedStack(labels[COMPOSE_PROJECT_LABEL]) &&
    labels[MANAGED_LABEL] === "true"
  );
}

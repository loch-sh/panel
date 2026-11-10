import { describe, expect, it } from "vitest";
import { hostingConfig } from "../../../../tests/mocks/fixtures";
import { defaultFormValues, formValuesToPayload } from "./formValues";

describe("defaultFormValues", () => {
  it("preselects the default networks of the server for a new site", () => {
    expect(defaultFormValues(hostingConfig(true)).networks).toEqual([
      "services",
    ]);
    expect(defaultFormValues(hostingConfig(false)).networks).toEqual([]);
  });

  it("starts from the server defaults", () => {
    const v = defaultFormValues(hostingConfig());
    expect(v).toMatchObject({
      template: "php-nginx",
      phpVersion: "8.4",
      documentRoot: "web",
      resources: { memoryMB: 512, cpus: 1 },
    });
    // The configured time zone wins over the browser's.
    expect(v.php?.timezone).toBe("Europe/Paris");
  });

  it("sends the chosen networks, sorted", () => {
    const v = {
      ...defaultFormValues(hostingConfig()),
      name: "blog",
      networks: ["services", "backend"],
    };
    expect(formValuesToPayload(v)).toMatchObject({
      name: "blog",
      networks: ["backend", "services"],
    });
  });
});

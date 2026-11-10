import { AxiosError, AxiosHeaders } from "axios";
import { beforeEach, describe, expect, it } from "vitest";
import i18n from "../i18n";
import { getApiErrorMessage } from "./apiErrors";

function apiError(data: unknown): AxiosError {
  const config = { headers: new AxiosHeaders() };
  return new AxiosError("Request failed", "ERR_BAD_REQUEST", config, null, {
    status: 409,
    statusText: "Conflict",
    headers: {},
    config,
    data,
  });
}

describe("getApiErrorMessage", () => {
  beforeEach(() => i18n.changeLanguage("en"));

  it("translates a known code", () => {
    expect(
      getApiErrorMessage(apiError({ code: "SERVICE_OPTION_IMMUTABLE" })),
    ).toBe("This setting cannot be changed after creation.");
  });

  it("uses the current language", async () => {
    await i18n.changeLanguage("fr");
    expect(
      getApiErrorMessage(apiError({ code: "SERVICE_OPTION_IMMUTABLE" })),
    ).toBe("Ce réglage ne peut pas être modifié après la création.");
  });

  it("never shows the raw server message of an unknown code", () => {
    const msg = getApiErrorMessage(
      apiError({ code: "SOMETHING_NEW", error: "internal detail /var/lib" }),
    );
    expect(msg).toBe(i18n.t("errors:fallback"));
    expect(msg).not.toContain("/var/lib");
  });

  it("falls back for errors that are not API errors", () => {
    expect(getApiErrorMessage(new Error("boom"), "Custom")).toBe("Custom");
  });
});

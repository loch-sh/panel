import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  CronJobModel,
  CronJobStatus,
  CronJobType,
} from "../../Services/Cron/Models/cron.model";
import { restrictedUser } from "../../../tests/mocks/fixtures";
import { mockApi } from "../../../tests/setup/msw";
import { renderWithProviders } from "../../../tests/setup/render";
import Cron from "./Cron";

const job = (over: Partial<CronJobModel>): CronJobModel => ({
  id: "j1",
  name: "nightly",
  schedule: "0 3 * * *",
  type: CronJobType.FUNCTION_INVOKE,
  enabled: true,
  once: false,
  functionInvoke: { function: "thumbs", event: '{"bucket":"media"}' },
  lastStatus: CronJobStatus.SUCCESS,
  lastRun: "2026-11-05T03:00:00Z",
  lastOutput: '{"statusCode":200,"durationMs":12,"payloadBytes":34}',
  createdAt: "2026-11-01T00:00:00Z",
  updatedAt: "2026-11-01T00:00:00Z",
  ...over,
});

const drawer = async () =>
  (await screen.findByText("New scheduled task")).closest(
    ".ant-drawer",
  ) as HTMLElement;

/** A wide screen: the columns hidden on phones are shown. */
function wideScreen() {
  const original = window.matchMedia;
  vi.spyOn(window, "matchMedia").mockImplementation((query: string) => ({
    ...original(query),
    matches: true,
  }));
}

describe("Cron (function invocations)", () => {
  afterEach(() => vi.restoreAllMocks());

  it("creates a function invocation from a function's page", async () => {
    renderWithProviders(<Cron />, { url: "/cron?newFunction=thumbs" });
    const form = await drawer();
    // Prefilled: the type and the function.
    await waitFor(() =>
      expect(within(form).getByText("Function invocation")).toBeInTheDocument(),
    );
    expect(within(form).getByTitle("thumbs")).toBeInTheDocument();

    await userEvent.type(within(form).getByLabelText("Name"), "Nightly thumbs");
    await userEvent.type(
      within(form).getByLabelText("Cron expression"),
      "0 3 * * *",
    );
    const event = within(form).getByLabelText("Event (JSON)");
    fireEvent.change(event, { target: { value: "{oops" } });
    await userEvent.click(within(form).getByRole("button", { name: "Create" }));
    expect(
      await within(form).findByText("The event must be one valid JSON value"),
    ).toBeInTheDocument();
    expect(mockApi().calls("POST", "/cron")).toHaveLength(0);

    fireEvent.change(event, { target: { value: '{"size": 64}' } });
    await userEvent.click(within(form).getByRole("button", { name: "Create" }));
    await waitFor(() =>
      expect(mockApi().calls("POST", "/cron")).toHaveLength(1),
    );
    expect(mockApi().calls("POST", "/cron")[0].body).toEqual({
      name: "Nightly thumbs",
      schedule: "0 3 * * *",
      type: "function_invoke",
      enabled: true,
      once: false,
      functionInvoke: { function: "thumbs", event: '{"size": 64}' },
    });
  });

  it("refuses an event over 64 KB", async () => {
    renderWithProviders(<Cron />, { url: "/cron?newFunction=thumbs" });
    const form = await drawer();
    fireEvent.change(within(form).getByLabelText("Event (JSON)"), {
      target: { value: JSON.stringify({ data: "x".repeat(70_000) }) },
    });
    await userEvent.click(within(form).getByRole("button", { name: "Create" }));
    expect(
      await within(form).findByText("The event is larger than 64 KB"),
    ).toBeInTheDocument();
  });

  it("shows the function jobs, the run summary and why the panel disabled one", async () => {
    mockApi().state.cronJobs = [
      job({}),
      job({
        id: "j2",
        name: "orphan",
        enabled: false,
        disabledReason: "FUNCTION_DELETED",
        lastSkipped: "2026-11-05T04:00:00Z",
        functionInvoke: { function: "gone", event: "{}" },
      }),
    ];
    wideScreen();
    renderWithProviders(<Cron />, { url: "/cron" });
    const nightly = (await screen.findByText("nightly")).closest(
      "tr",
    ) as HTMLElement;
    expect(
      within(nightly).getByText('λ thumbs {"bucket":"media"}'),
    ).toBeInTheDocument();
    const orphan = screen.getByText("orphan").closest("tr") as HTMLElement;
    expect(
      within(orphan).getByText("Disabled by the panel"),
    ).toBeInTheDocument();
    expect(within(orphan).getByText("Run skipped")).toBeInTheDocument();

    // The run summary (never the payload).
    await userEvent.click(
      within(nightly).getByRole("button", { name: /expand/i }),
    );
    expect(
      await screen.findByText("12 ms, response of 34 bytes"),
    ).toBeInTheDocument();
    await userEvent.click(
      within(orphan).getByRole("button", { name: /expand/i }),
    );
    expect(
      await screen.findByText(/the function it invoked was deleted/),
    ).toBeInTheDocument();
  });

  it("offers no function invocation to a restricted user", async () => {
    mockApi().state.me = restrictedUser;
    renderWithProviders(<Cron />, { url: "/cron" });
    await userEvent.click(
      await screen.findByRole("button", { name: /New task/ }),
    );
    const form = await drawer();
    await userEvent.click(within(form).getByRole("combobox", { name: "Type" }));
    expect(await screen.findAllByText("Docker command")).not.toHaveLength(0);
    expect(screen.queryByText("Function invocation")).not.toBeInTheDocument();
    expect(mockApi().calls("GET", "/functions/config")).toHaveLength(0);
  });

  it("renders the error and the output of a run as text", async () => {
    const xss =
      '<script>window.__pwned = true</script><img src=x onerror="window.__pwned = true">';
    mockApi().state.cronJobs = [
      job({ lastStatus: CronJobStatus.ERROR, lastError: xss, lastOutput: xss }),
      job({
        id: "j2",
        name: "http",
        type: CronJobType.HTTP,
        functionInvoke: undefined,
        http: { method: "GET", url: "https://example.com" },
        lastOutput: xss,
      }),
    ];
    renderWithProviders(<Cron />, { url: "/cron" });
    for (const name of ["nightly", "http"]) {
      const r = (await screen.findByText(name)).closest("tr") as HTMLElement;
      await userEvent.click(within(r).getByRole("button", { name: /expand/i }));
    }
    expect((await screen.findAllByText(xss)).length).toBeGreaterThanOrEqual(3);
    expect(document.querySelector("script")).toBeNull();
    expect(document.querySelector('img[src="x"]')).toBeNull();
    expect((window as { __pwned?: boolean }).__pwned).toBeUndefined();
  });
});

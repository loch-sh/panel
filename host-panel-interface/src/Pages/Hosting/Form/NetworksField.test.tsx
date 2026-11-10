import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Form } from "antd";
import { describe, expect, it, vi } from "vitest";
import { mockApi } from "../../../../tests/setup/msw";
import { renderWithProviders } from "../../../../tests/setup/render";
import NetworksField from "./NetworksField";

function TestForm({
  initial,
  onSubmit,
}: Readonly<{ initial: string[]; onSubmit: (v: string[]) => void }>) {
  const [form] = Form.useForm<{ networks: string[] }>();
  return (
    <Form
      form={form}
      initialValues={{ networks: initial }}
      onFinish={(v) => onSubmit(v.networks)}
    >
      <NetworksField />
      <button type="submit">submit</button>
    </Form>
  );
}

describe("NetworksField (site form)", () => {
  it("offers the networks of the API and keeps the chosen ones", async () => {
    const onSubmit = vi.fn();
    renderWithProviders(
      <TestForm initial={["services"]} onSubmit={onSubmit} />,
    );
    const select = screen.getByRole("combobox", { name: "Networks" });
    await waitFor(() =>
      expect(mockApi().calls("GET", "/networks")).toHaveLength(1),
    );
    await userEvent.click(select);
    await userEvent.click(
      await screen.findByText("backend", {
        selector: ".ant-select-item-option-content *",
      }),
    );
    await userEvent.click(screen.getByRole("button", { name: "submit" }));
    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(["services", "backend"]),
    );
    expect(
      screen.getByRole("link", { name: "Create a private network" }),
    ).toHaveAttribute("href", "/networks/new");
  });
});

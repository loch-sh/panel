import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { S3_ACCESS_KEY, S3_SECRET } from "../../../tests/mocks/fixtures";
import CredentialsModal from "./CredentialsModal";

const s3 = [
  { key: "endpoint", value: "http://svc-s3-media:8080" },
  { key: "region", value: "us-east-1" },
  { key: "accessKeyId", value: S3_ACCESS_KEY },
  { key: "secretAccessKey", value: S3_SECRET, secret: true },
  { key: "bucket", value: "media" },
  { key: "pathStyle", value: "true" },
];

describe("CredentialsModal", () => {
  it("masks the secret until revealed", async () => {
    render(<CredentialsModal credentials={s3} onClose={() => {}} />);
    const dialog = await screen.findByRole("dialog", {
      name: "Connection details",
    });
    expect(within(dialog).getByText("Secret access key")).toBeInTheDocument();
    expect(within(dialog).queryByText(S3_SECRET)).not.toBeInTheDocument();
    await userEvent.click(
      within(dialog).getByRole("button", { name: /show/i }),
    );
    expect(within(dialog).getByText(S3_SECRET)).toBeInTheDocument();
  });

  it("offers the Laravel .env lines for S3 only", async () => {
    const { unmount } = render(
      <CredentialsModal credentials={s3} onClose={() => {}} />,
    );
    expect(await screen.findByText(/Laravel \.env/)).toBeInTheDocument();
    unmount();
    render(
      <CredentialsModal
        credentials={[
          { key: "host", value: "svc-redis-cache" },
          { key: "password", value: S3_SECRET, secret: true },
        ]}
        onClose={() => {}}
      />,
    );
    await screen.findByRole("dialog");
    expect(screen.queryByText(/Laravel \.env/)).not.toBeInTheDocument();
  });

  it("cannot be dismissed by accident (the secret is shown once)", async () => {
    const onClose = vi.fn();
    render(<CredentialsModal credentials={s3} onClose={onClose} />);
    const dialog = await screen.findByRole("dialog");
    // Escape from inside the dialog (where rc-dialog listens), then a click
    // on the mask.
    await userEvent.click(within(dialog).getByText("Secret access key"));
    await userEvent.keyboard("{Escape}");
    const mask = document.querySelector(".ant-modal-wrap");
    expect(mask).not.toBeNull();
    await userEvent.click(mask as Element);
    expect(onClose).not.toHaveBeenCalled();
    await userEvent.click(
      screen.getByRole("button", { name: "I have copied the password" }),
    );
    expect(onClose).toHaveBeenCalledOnce();
  });
});

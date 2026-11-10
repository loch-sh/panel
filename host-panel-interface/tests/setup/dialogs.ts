import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

/**
 * A dialog by its title (antd dialogs all get the same aria-labelledby id
 * under test, so the accessible name is not reliable).
 */
export async function dialogTitled(
  title: string | RegExp,
): Promise<HTMLElement> {
  // The same text may also be on the page (a card title): wait for a dialog.
  return waitFor(() => {
    const dialog = screen
      .getAllByText(title)
      .map((m) => m.closest('[role="dialog"]'))
      .find((d): d is HTMLElement => d !== null);
    if (!dialog) throw new Error(`No dialog titled ${String(title)}`);
    return dialog;
  });
}

/** Opens an antd Select and picks an option by its text. */
export async function choose(combobox: HTMLElement, option: RegExp | string) {
  await userEvent.click(combobox);
  const item = await screen.findByText(option, {
    selector:
      ".ant-select-item-option-content, .ant-select-item-option-content *",
  });
  await userEvent.click(item);
}

// SPDX-License-Identifier: AGPL-3.0-or-later

// A drive list's own strings that fail to load show an error with Try again, and Try again loads them (tl4 r1, finding 10).
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

const failRu = vi.hoisted(() => ({ on: true }));
vi.mock("../../i18n/drive/ru.json", async () => {
  if (failRu.on) throw new Error("chunk failed");
  return await vi.importActual<{ default: Record<string, string> }>("../../i18n/drive/ru.json");
});

import { setLang } from "../i18n";
import { useDriveText } from "./driveText";

function Probe() {
  const drive = useDriveText();
  if (drive.ready) return <p>ready</p>;
  if (drive.failed) return <button onClick={drive.retry}>Try again</button>;
  return <p>loading</p>;
}

afterEach(async () => {
  await setLang("en");
});

describe("useDriveText", () => {
  it("offers Try again when the drive text does not load, and loads it on Try again", async () => {
    await setLang("ru");
    render(<Probe />);
    fireEvent.click(await screen.findByRole("button", { name: "Try again" }));
    failRu.on = false;
    expect(await screen.findByText("ready")).toBeTruthy();
  });
});

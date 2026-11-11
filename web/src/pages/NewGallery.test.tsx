// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { App as AntApp } from "antd";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../lib/session";
import type { Here } from "../lib/files/location";
import type { Node } from "../lib/files/types";

const FLD = "fld_abcdefgh";
const NOD = "nod_abcdefgh";
const ROOT = "nod_rootroot";
const ACC_ITEM = "nod_accacc01";

const m = vi.hoisted(() => ({
  listAllTemplates: vi.fn(),
  createFromTemplate: vi.fn(),
  locate: vi.fn(),
  myFolders: vi.fn(),
  createDocument: vi.fn(),
}));

vi.mock("../lib/files/templates", async (importOriginal) => {
  const real = await importOriginal<typeof import("../lib/files/templates")>();
  return { ...real, listAllTemplates: m.listAllTemplates, createFromTemplate: m.createFromTemplate };
});
vi.mock("../lib/files/location", async (importOriginal) => {
  const real = await importOriginal<typeof import("../lib/files/location")>();
  return { ...real, locate: m.locate };
});
vi.mock("../lib/files/api", async (importOriginal) => {
  const real = await importOriginal<typeof import("../lib/files/api")>();
  return { ...real, myFolders: m.myFolders, createDocument: m.createDocument };
});

import NewGallery from "./NewGallery";

const BUILTINS = [
  { id: "builtin:letter", name: "Letter.docx", kind: "document", source: "builtin", category: "letters", has_thumbnail: true },
  { id: "builtin:lo-letter-serif", name: "Modern letter", kind: "document", source: "builtin", category: "letters", has_thumbnail: true },
  { id: "builtin:resume", name: "Resume.docx", kind: "document", source: "builtin", category: "resumes", has_thumbnail: true },
  { id: "builtin:meeting-notes", name: "Meeting notes.docx", kind: "document", source: "builtin", category: "reports", has_thumbnail: true },
  { id: "builtin:budget", name: "Budget.xlsx", kind: "spreadsheet", source: "builtin", category: "workbooks", has_thumbnail: true },
  { id: "builtin:slide-deck", name: "Slide deck.pptx", kind: "presentation", source: "builtin", category: "presentations", has_thumbnail: true },
  { id: "builtin:lo-beehive", name: "Beehive", kind: "presentation", source: "builtin", category: "designs", has_thumbnail: true },
];
const ACCOUNT = { id: ACC_ITEM, name: "Team letter.docx", kind: "document", source: "account", node_id: ACC_ITEM, has_thumbnail: false };

function here(role: string, dirID: string | null): Here {
  return {
    folder: FLD,
    folderName: "Team A",
    folderRole: role as Here["folderRole"],
    dir: dirID ? ({ id: dirID, name: "Projects" } as Node) : null,
    dirRole: role as Here["dirRole"],
    trail: [],
    rootReachable: true,
    truncated: false,
    reach: [],
    spaceNoDownload: false,
    path: dirID ? "/Projects" : "/",
    rootNodeID: ROOT,
  };
}

function signedAs(role: string): Session {
  return {
    user: { id: "u1", email: "u1@example.com", display_name: "u1", role: "member", locale: "en" } as Session["user"],
    account: null,
    csrf: "t",
    publicLinks: false,
    _role: role,
  } as Session;
}

function renderAt(path: string, role = "editor") {
  session.set(signedAs(role));
  m.locate.mockImplementation(async (_f: string, d: string | null) => here(role, d));
  m.myFolders.mockResolvedValue([{ id: FLD, name: "Team A", kind: "team", role, access: "write" }]);
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <AntApp>
        <MemoryRouter initialEntries={[path]}>
          <NewGallery />
        </MemoryRouter>
      </AntApp>
    </QueryClientProvider>,
  );
}

// sections lists the sections on the page in their order.
function sections(): string[] {
  return Array.from(document.querySelectorAll("[data-section]")).map((el) => el.getAttribute("data-section") ?? "");
}

beforeEach(() => {
  m.listAllTemplates.mockReset();
  m.createFromTemplate.mockReset();
  m.createFromTemplate.mockResolvedValue({ id: "nod_newnewnw", name: "Letter.docx" });
  m.createDocument.mockReset();
  m.createDocument.mockResolvedValue({ id: "nod_blankblk", name: "Untitled.xlsx" });
  m.locate.mockReset();
  m.myFolders.mockReset();
  vi.spyOn(window, "open").mockImplementation(() => ({ opener: null }) as unknown as Window);
});
afterEach(() => {
  session.set(null);
  vi.restoreAllMocks();
});

describe("NewGallery: the sections", () => {
  it("shows Blank first, then only the categories of the kind asked, in order", async () => {
    m.listAllTemplates.mockResolvedValue(BUILTINS);
    renderAt(`/new/${FLD}/${NOD}?kind=document`);
    await waitFor(() => expect(sections().length).toBeGreaterThan(1));
    expect(sections()).toEqual(["blank", "letters", "resumes", "reports"]);
    expect(m.listAllTemplates).toHaveBeenCalledWith("document");
  });

  it("a spreadsheet shows its workbooks only", async () => {
    m.listAllTemplates.mockResolvedValue(BUILTINS);
    renderAt(`/new/${FLD}?kind=spreadsheet`);
    await waitFor(() => expect(sections()).toContain("workbooks"));
    expect(sections()).toEqual(["blank", "workbooks"]);
  });

  it("a presentation shows presentations, then designs", async () => {
    m.listAllTemplates.mockResolvedValue(BUILTINS);
    renderAt(`/new/${FLD}?kind=presentation`);
    await waitFor(() => expect(sections()).toContain("designs"));
    expect(sections()).toEqual(["blank", "presentations", "designs"]);
  });

  it("shows the account's folder section after the built-ins, only when the account has templates", async () => {
    m.listAllTemplates.mockResolvedValue([...BUILTINS.slice(0, 1), ACCOUNT]);
    renderAt(`/new/${FLD}?kind=document`);
    await waitFor(() => expect(sections()).toContain("mine"));
    expect(sections()).toEqual(["blank", "letters", "mine"]);
  });

  it("shows no account section when the account has none", async () => {
    m.listAllTemplates.mockResolvedValue(BUILTINS);
    renderAt(`/new/${FLD}?kind=document`);
    await waitFor(() => expect(sections()).toContain("letters"));
    expect(sections()).not.toContain("mine");
  });

  // The design: Blank does not need the template list, so a failed list still leaves Blank (with the error and a retry).
  it("a failed template list still shows Blank, with the error", async () => {
    m.listAllTemplates.mockRejectedValue(new Error("list failed"));
    renderAt(`/new/${FLD}?kind=document`);
    await waitFor(() => expect(sections()).toEqual(["blank"]));
    expect(await screen.findByRole("alert")).toBeTruthy();
  });

  it("an empty listing still shows Blank", async () => {
    m.listAllTemplates.mockResolvedValue([]);
    renderAt(`/new/${FLD}?kind=document`);
    await waitFor(() => expect(sections()).toEqual(["blank"]));
  });
});

describe("NewGallery: the tiles", () => {
  it("a built-in shows its thumbnail, with the name as alt text and lazy loading", async () => {
    m.listAllTemplates.mockResolvedValue(BUILTINS);
    renderAt(`/new/${FLD}?kind=document`);
    const tile = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-template="builtin:letter"]');
      expect(el).not.toBeNull();
      return el!;
    });
    const img = within(tile).getByRole("img") as HTMLImageElement;
    expect(img.getAttribute("src")).toBe("/api/v1/templates/builtin%3Aletter/thumbnail");
    expect(img.getAttribute("alt")).toBe("Letter.docx");
    expect(img.getAttribute("loading")).toBe("lazy");
  });

  it("an account template shows a document icon and no picture", async () => {
    m.listAllTemplates.mockResolvedValue([ACCOUNT]);
    renderAt(`/new/${FLD}?kind=document`);
    const tile = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(`[data-template="${ACC_ITEM}"]`);
      expect(el).not.toBeNull();
      return el!;
    });
    expect(tile.querySelector("img")).toBeNull();
    expect(tile.querySelector(".lucide-file-text")).not.toBeNull();
  });
});

describe("NewGallery: a click makes the file", () => {
  it("posts the template to the directory shown, then opens the file as New's blank document does", async () => {
    m.listAllTemplates.mockResolvedValue(BUILTINS);
    renderAt(`/new/${FLD}/${NOD}?kind=document`);
    const tile = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-template="builtin:letter"]');
      expect(el).not.toBeNull();
      return el!;
    });
    fireEvent.click(tile);
    await waitFor(() => expect(m.createFromTemplate).toHaveBeenCalledTimes(1));
    expect(m.createFromTemplate).toHaveBeenCalledWith(NOD, "builtin:letter");
    await waitFor(() => expect(window.open).toHaveBeenCalledWith(expect.stringContaining("/edit/wait"), "_blank"));
  });

  it("with no directory, posts to the folder's root", async () => {
    m.listAllTemplates.mockResolvedValue(BUILTINS);
    renderAt(`/new/${FLD}?kind=document`);
    const tile = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-template="builtin:letter"]');
      expect(el).not.toBeNull();
      return el!;
    });
    fireEvent.click(tile);
    await waitFor(() => expect(m.createFromTemplate).toHaveBeenCalledTimes(1));
    expect(m.createFromTemplate).toHaveBeenCalledWith(ROOT, "builtin:letter");
  });

  it("Blank makes a blank file the same way, with no template", async () => {
    m.listAllTemplates.mockResolvedValue(BUILTINS);
    renderAt(`/new/${FLD}/${NOD}?kind=spreadsheet`);
    const blank = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-section="blank"] button');
      expect(el).not.toBeNull();
      return el!;
    });
    fireEvent.click(blank);
    await waitFor(() => expect(m.createDocument).toHaveBeenCalledWith(NOD, "xlsx"));
    expect(m.createFromTemplate).not.toHaveBeenCalled();
  });
});

describe("NewGallery: states", () => {
  it("loading shows six skeleton tiles", async () => {
    m.listAllTemplates.mockReturnValue(new Promise(() => {}));
    renderAt(`/new/${FLD}?kind=document`);
    await waitFor(() => expect(document.querySelectorAll('[data-testid="gallery-skeleton"]').length).toBe(6));
  });

  it("an error shows its message and a retry that asks again", async () => {
    m.listAllTemplates.mockRejectedValueOnce(new Error("boom"));
    renderAt(`/new/${FLD}?kind=document`);
    const retry = await screen.findByRole("button", { name: /retry|try again|files\.gallery\.retry/i });
    expect(screen.getByRole("alert")).toBeTruthy();
    m.listAllTemplates.mockResolvedValue(BUILTINS);
    await act(async () => fireEvent.click(retry));
    await waitFor(() => expect(sections()).toContain("letters"));
    expect(m.listAllTemplates).toHaveBeenCalledTimes(2);
  });

  it("an unknown kind is the not-found page, and nothing is listed", async () => {
    m.listAllTemplates.mockResolvedValue(BUILTINS);
    renderAt(`/new/${FLD}?kind=drawing`);
    await waitFor(() => expect(screen.getByText("Page not found")).toBeTruthy());
    expect(m.listAllTemplates).not.toHaveBeenCalled();
    expect(document.querySelector("[data-section]")).toBeNull();
  });
});

describe("NewGallery: a viewer's place", () => {
  it("a viewer sees the viewer note, a link back to the folder, and no template", async () => {
    m.listAllTemplates.mockResolvedValue(BUILTINS);
    renderAt(`/new/${FLD}?kind=document`, "viewer");
    const note = await screen.findByTestId("gallery-viewer-note");
    expect(note).toBeTruthy();
    const back = within(note).getByRole("link") as HTMLAnchorElement;
    expect(back.getAttribute("href")).toBe(`/files/${FLD}`);
    expect(document.querySelectorAll("[data-template]").length).toBe(0);
    expect(document.querySelectorAll("[data-section]").length).toBe(0);
  });
});

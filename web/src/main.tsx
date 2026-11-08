// SPDX-License-Identifier: AGPL-3.0-or-later

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "react-router-dom";
import { App as AntdApp } from "antd";
import "./index.css";
import { clearOnSessionChange, createQueryClient } from "./lib/query";
import { listenOfficeChanges } from "./lib/files/officeSync";
import { createAppRouter } from "./lib/router";
import { loadLang } from "./lib/i18n";
import { ThemeProvider } from "./theme/theme";
import ErrorBoundary from "./components/ErrorBoundary";
import { Boot, Direction, Gate } from "./App";
import { listenForInstall } from "./lib/pwa/install";
import { registerWorker } from "./lib/pwa/worker";
import UpdatePrompt from "./components/pwa/UpdatePrompt";

// The install offer is caught as the page starts, before the first render, so the browser's early offer is not missed (decision 65).
listenForInstall();
registerWorker();

const queryClient = createQueryClient();
clearOnSessionChange(queryClient);
// The other tabs read again what an office editor changed in this browser (decision 107).
listenOfficeChanges(queryClient);
const router = createAppRouter([{ path: "*", element: <Boot><Gate /></Boot> }]);

const root = document.getElementById("root");
if (!root) throw new Error("the page has no #root element");
// showRetry replaces the page when English cannot be read, twice: a deploy that replaced the file, or a dropped connection. The
// catalogue is not there to name the words, so this one notice is in English and needs no catalogue; the button reloads the
// page, which reads the new address of the file.
function showRetry(target: HTMLElement) {
  const text = document.createElement("p");
  text.textContent = "This page could not load. Check the connection, then try again.";
  const button = document.createElement("button");
  button.type = "button";
  button.textContent = "Try again";
  button.addEventListener("click", () => window.location.reload());
  target.replaceChildren(text, button);
}

// English is the fallback for every language, so its catalogue is read before the first render: no raw key shows. One retry
// covers a dropped connection; a second failure shows the retry notice above instead of a blank page.
void loadLang("en")
  .catch(() => loadLang("en"))
  .then(
    () => {
      createRoot(root).render(
        <StrictMode>
          <QueryClientProvider client={queryClient}>
            <ThemeProvider>
              <AntdApp>
                <Direction>
                  <ErrorBoundary>
                    <RouterProvider router={router} />
                  </ErrorBoundary>
                  <UpdatePrompt />
                </Direction>
              </AntdApp>
            </ThemeProvider>
          </QueryClientProvider>
        </StrictMode>,
      );
    },
    () => showRetry(root),
  );

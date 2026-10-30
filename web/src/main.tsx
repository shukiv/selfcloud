// SPDX-License-Identifier: AGPL-3.0-or-later

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "react-router-dom";
import "./index.css";
import { clearOnSessionChange, createQueryClient } from "./lib/query";
import { createAppRouter } from "./lib/router";
import { ThemeProvider } from "./theme/theme";
import ErrorBoundary from "./components/ErrorBoundary";
import { Boot, Direction, Gate } from "./App";

const queryClient = createQueryClient();
clearOnSessionChange(queryClient);
const router = createAppRouter([{ path: "*", element: <Boot><Gate /></Boot> }]);

const root = document.getElementById("root");
if (!root) throw new Error("the page has no #root element");
createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <Direction>
          <ErrorBoundary>
            <RouterProvider router={router} />
          </ErrorBoundary>
        </Direction>
      </ThemeProvider>
    </QueryClientProvider>
  </StrictMode>,
);

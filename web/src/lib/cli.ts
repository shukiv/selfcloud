// SPDX-License-Identifier: AGPL-3.0-or-later

import { api } from "./api";

// The command-line client's device sign-in (decision 125). The approval page types the code a terminal shows, and
// approves the sign-in for the person whose session this browser holds. Manage lists that person's devices and ends one.

// CliRequest is a pending terminal sign-in, as the approval page shows it before anyone approves it. The token is never in it.
export interface CliRequest {
  client_name: string;
  client_ip: string;
  user_agent: string;
  created_at: string;
  expires_at: string;
}

// CliDevice is one device of the signed-in person that still works.
export interface CliDevice {
  id: string;
  client_name: string;
  created_at: string;
  expires_at: string;
}

// cliLookup shows the pending sign-in that the typed code names. The code is the one the terminal shows; a wrong code counts
// against the request it names, as the server counts it.
export function cliLookup(userCode: string): Promise<CliRequest> {
  return api<CliRequest>("POST", "/api/v1/auth/cli/lookup", { body: { user_code: userCode } });
}

// cliApprove approves the sign-in that the typed code names, for the signed-in person.
export function cliApprove(userCode: string): Promise<{ status: string }> {
  return api<{ status: string }>("POST", "/api/v1/auth/cli/approve", { body: { user_code: userCode } });
}

// cliDevices lists the signed-in person's devices that still work, newest first.
export function cliDevices(): Promise<{ devices: CliDevice[] }> {
  return api<{ devices: CliDevice[] }>("GET", "/api/v1/auth/cli/devices");
}

// cliRevokeDevice ends one of the signed-in person's devices. Its token stops on the next call.
export function cliRevokeDevice(id: string): Promise<void> {
  return api<void>("DELETE", `/api/v1/auth/cli/devices/${encodeURIComponent(id)}`);
}

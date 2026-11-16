// SPDX-License-Identifier: AGPL-3.0-or-later

// Shapes of the API responses the interface reads. Only the fields it uses are listed.
export interface User {
  id: string;
  email: string;
  display_name: string;
  locale: string;
  role: 'owner' | 'admin' | 'member' | string;
  status: string;
}

export interface Account {
  id: string;
  name: string;
}

export interface Me {
  user: User;
  account: Account | null;
  csrf_token: string;
  personal_folder_id: string | null;
  // public_links is the account's switch for links that anyone can open. False for an installation admin.
  public_links?: boolean;
  // must_change_password: the person must choose a new password before anything else.
  must_change_password?: boolean;
  // impersonation is present only while an installation admin is signed in as this person; ends_at is the hard limit.
  impersonation?: { ends_at: string };
  // tenant_access and mail_configured are sent only to an installation admin's own session.
  tenant_access?: boolean;
  mail_configured?: boolean;
  // email_proved: the person has proved the address of their session. account_external_sharing: the account's
  // switch for sharing with people in other accounts. Absent in an answer from an older server.
  email_proved?: boolean;
  account_external_sharing?: boolean;
  // frame: this session was started by a frame sign-in link, so the page is embedded in a host application
  // (decision 197). frame_origins are the origins that may frame the account's pages, present only for a frame
  // session (decision 214). Absent in an answer from an older server.
  frame?: boolean;
  frame_origins?: string[];
}

export interface SignedIn {
  user: User;
  csrf_token: string;
}

// SignedInLink is a sign-in by link: the session, and the path the page opens next. frame and frame_origins are
// present only when the link started a frame session (decisions 197, 214).
export interface SignedInLink extends SignedIn {
  next: string;
  frame?: boolean;
  frame_origins?: string[];
}

export interface AccountChoice {
  id: string;
  name: string;
}

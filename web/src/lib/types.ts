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
}

export interface SignedIn {
  user: User;
  csrf_token: string;
}

// SignedInLink is a sign-in by link: the session, and the path the page opens next.
export interface SignedInLink extends SignedIn {
  next: string;
}

export interface AccountChoice {
  id: string;
  name: string;
}

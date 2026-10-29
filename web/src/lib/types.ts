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
}

export interface SignedIn {
  user: User;
  csrf_token: string;
}

export interface AccountChoice {
  id: string;
  name: string;
}

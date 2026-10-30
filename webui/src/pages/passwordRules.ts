// SPDX-License-Identifier: AGPL-3.0-or-later

// PasswordCheck is what is wrong with a new password and its confirmation: a catalogue key for each
// field, or null when it is right. The length counts characters, not bytes.
export interface PasswordCheck {
  password: string | null;
  confirm: string | null;
}

export function checkNewPassword(password: string, confirm: string, minLength: number): PasswordCheck {
  const length = [...password].length;
  if (length < minLength) return { password: "error.passwordShort", confirm: null };
  if (length > 256) return { password: "error.passwordLong", confirm: null };
  if (password !== confirm) return { password: null, confirm: "error.passwordMismatch" };
  return { password: null, confirm: null };
}

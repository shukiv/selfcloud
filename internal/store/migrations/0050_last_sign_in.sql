-- When each user last signed in (decision 114, slice tl1): the admin lists show it as Last sign-in. A session is deleted
-- at its expiry, so the time is kept on the user, where it outlives the session. It is set when a session of the user's
-- own is made (a password sign-in, a verification or reset link, a sign-in link, a device confirmation, a password
-- change); a sign-in-as is the admin's and does not set it. NULL: the user has not signed in since this column was added.

ALTER TABLE users ADD COLUMN last_sign_in_at INTEGER;

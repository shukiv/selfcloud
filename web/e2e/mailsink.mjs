// SPDX-License-Identifier: AGPL-3.0-or-later

// The mail sink of the browser runs: a minimal SMTP server that keeps the mail it is sent, so a run can take the links
// from it. It does no TLS and no login: the server dials it with smtp_tls none, on localhost.
import { createServer } from 'node:net';

export function mailSink() {
  const messages = [];
  const server = createServer((sock) => {
    let buf = '';
    let to = [];
    let data = null;
    const say = (line) => sock.write(`${line}\r\n`);
    sock.on('error', () => {});
    say('220 e2e-sink');
    sock.on('data', (chunk) => {
      buf += chunk.toString('latin1');
      let end;
      while ((end = buf.indexOf('\r\n')) !== -1) {
        const line = buf.slice(0, end);
        buf = buf.slice(end + 2);
        if (data !== null) {
          if (line !== '.') {
            data += `${line}\r\n`;
            continue;
          }
          messages.push({ to, data });
          to = [];
          data = null;
          say('250 queued');
          continue;
        }
        const verb = line.slice(0, 4).toUpperCase();
        if (verb === 'RCPT') {
          to.push(/<([^>]*)>/.exec(line)?.[1] ?? line.slice(8));
          say('250 ok');
        } else if (verb === 'DATA') {
          data = '';
          say('354 end with a dot');
        } else if (verb === 'RSET') {
          to = [];
          say('250 ok');
        } else if (verb === 'QUIT') {
          say('221 bye');
          sock.end();
        } else {
          say('250 ok');
        }
      }
    });
  });
  return new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () =>
      resolve({ port: server.address().port, messages, close: () => server.close() }),
    );
  });
}

// waitForMail waits for a message to the address, and returns it.
export async function waitForMail(sink, address, timeout = 15000) {
  const until = Date.now() + timeout;
  for (;;) {
    const m = sink.messages.find((x) => x.to.includes(address));
    if (m) return m;
    if (Date.now() > until) throw new Error(`no mail to ${address}`);
    await new Promise((r) => setTimeout(r, 100));
  }
}

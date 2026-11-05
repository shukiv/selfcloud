// SPDX-License-Identifier: AGPL-3.0-or-later

// A fake Collabora conversion interface for the browser run (slice s4b). It answers the way decision 37 records the live
// server does: discovery lists the edit actions on its own host and no convert-to; /hosting/capabilities says conversion is
// available; POST /cool/convert-to/<format> takes a multipart part named source.<ext> and answers the converted bytes, with
// the type application/octet-stream and nosniff. A conversion between two families (writer, calc, impress) that is not PDF
// is answered 500 with X-Error-Kind: savefailed, as the live server answers a document it cannot save. The run can refuse
// every conversion with refuse(true), to show the failed state. It serves HTTPS on 127.0.0.1 with a certificate made here;
// the server under test trusts it through SSL_CERT_FILE, which the run sets.
import { execFile } from 'node:child_process';
import { generateKeyPairSync, sign } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { createServer } from 'node:https';
import { join } from 'node:path';
import { promisify } from 'node:util';

const execFileP = promisify(execFile);

// FAMILY maps the source types the fake knows to the family a conversion stays within.
const FAMILY = { docx: 'writer', odt: 'writer', xlsx: 'calc', ods: 'calc', pptx: 'impress', odp: 'impress' };
const FORMATS = ['pdf', 'docx', 'odt', 'xlsx', 'ods', 'pptx', 'odp', 'png'];

// b64 turns a base64url value into standard base64, as the discovery document names its key.
const b64 = (s) => Buffer.from(s, 'base64url').toString('base64');

// minimalPDF is a one-page PDF with correct offsets, so a viewer reads it without repair.
function minimalPDF() {
  const text = 'BT /F1 18 Tf 40 60 Td (Converted copy) Tj ET';
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 144] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>',
    `<< /Length ${text.length} >>\nstream\n${text}\nendstream`,
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  ];
  let out = '%PDF-1.4\n';
  const offsets = [];
  objects.forEach((body, i) => {
    offsets.push(Buffer.byteLength(out));
    out += `${i + 1} 0 obj\n${body}\nendobj\n`;
  });
  const xref = Buffer.byteLength(out);
  out += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`;
  for (const o of offsets) out += `${String(o).padStart(10, '0')} 00000 n \n`;
  out += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(out, 'latin1');
}

// The editor's page (decision 72). It stands in for Collabora's editor: a POST with the token in its body. The fake calls the
// WOPI host itself, with the token in the query and proof headers it signs with its own key (the same expected-proof layout
// the verifier reads), and it locks the file as an editor does. The page then tells its parent the document is loaded, and it
// has a close button that tells the parent to close. It does not show the token.
const DOT_NET_EPOCH_TICKS = 621355968000000000n;

// signProof signs the expected proof of a WOPI request with the fake's key: a 4-byte big-endian length and the bytes of the
// token, of the URL in uppercase, and of the timestamp (.NET ticks, 8 bytes big-endian).
function signProof(privateKey, token, url, ticks) {
  const part = (b) => Buffer.concat([Buffer.from([(b.length >>> 24) & 255, (b.length >>> 16) & 255, (b.length >>> 8) & 255, b.length & 255]), b]);
  const tick = Buffer.alloc(8);
  tick.writeBigInt64BE(ticks);
  const expected = Buffer.concat([part(Buffer.from(token, 'utf8')), part(Buffer.from(url.toUpperCase(), 'utf8')), part(tick)]);
  return sign('sha256', expected, privateKey).toString('base64');
}

const EDITOR_PATH = '/browser/42745e44dd/cool.html';

// openAsEditor calls the WOPI host the way an editor does: CheckFileInfo with the token in the query, then a LOCK when the
// caller may write. It returns what it saw, and nothing of the token.
async function openAsEditor(privateKey, token, wopiSrc) {
  const call = async (method, headers, payload) => {
    const target = new URL(wopiSrc);
    target.searchParams.set('access_token', token);
    const signed = target.toString();
    const ticks = BigInt(Date.now()) * 10000n + DOT_NET_EPOCH_TICKS;
    const proof = signProof(privateKey, token, signed, ticks);
    return fetch(signed, {
      method,
      headers: { 'X-WOPI-Timestamp': String(ticks), 'X-WOPI-Proof': proof, ...headers },
      body: payload,
    });
  };
  const info = await call('GET', {});
  if (!info.ok) return { name: '', userCanWrite: false, locked: false, checkStatus: info.status, userInfo: '', userInfoStatus: 0 };
  const body = await info.json();
  const writable = body.UserCanWrite === true;
  let locked = false;
  if (writable) {
    const lock = await call('POST', { 'X-WOPI-Override': 'LOCK', 'X-WOPI-Lock': 'fake-editor-lock' });
    locked = lock.status === 200;
  }
  // PutUserInfo: a host that says it supports it is given the editor's string, which it keeps for this person (decision 87).
  // userInfo is what the host held before this open, so a check can see the string of an earlier open.
  let userInfoStatus = 0;
  if (body.SupportsUserInfo === true) {
    userInfoStatus = (await call('POST', { 'X-WOPI-Override': 'PUT_USER_INFO' }, 'fake-user-info')).status;
  }
  return {
    name: body.BaseFileName ?? '',
    userCanWrite: writable,
    locked,
    checkStatus: info.status,
    userInfo: typeof body.UserInfo === 'string' ? body.UserInfo : '',
    userInfoStatus,
  };
}

// saveAsAsEditor makes the copy the way Collabora does once the person has picked its name: PutRelativeFile on the file the
// editor holds, with the name as a suggested target (UTF-7, where a plus sign is written +-; the e2e names are ASCII). It
// answers the status and the Name the host gives the copy.
async function saveAsAsEditor(privateKey, editor, filename) {
  const target = new URL(editor.wopiSrc);
  target.searchParams.set('access_token', editor.token);
  const signed = target.toString();
  const ticks = BigInt(Date.now()) * 10000n + DOT_NET_EPOCH_TICKS;
  const proof = signProof(privateKey, editor.token, signed, ticks);
  const res = await fetch(signed, {
    method: 'POST',
    headers: {
      'X-WOPI-Timestamp': String(ticks),
      'X-WOPI-Proof': proof,
      'X-WOPI-Override': 'PUT_RELATIVE',
      'X-WOPI-SuggestedTarget': filename.replace(/\+/g, '+-'),
    },
    body: 'a copy made by the fake editor',
  });
  const answer = res.ok ? await res.json() : {};
  return { status: res.status, name: answer.Name ?? '' };
}

// editorPage is the page the fake shows in the frame. It has no token in it: the token was only in the POST body.
function editorPage({ name, userCanWrite, locked }) {
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);
  return `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Fake editor</title></head>
<body style="font:16px sans-serif;margin:16px">
<p id="fake-editor-file">${esc(name)}</p>
<p id="fake-editor-write">${userCanWrite ? 'editable' : 'read only'}</p>
<p id="fake-editor-lock">${locked ? 'locked' : 'not locked'}</p>
<button id="fake-editor-saveas" type="button">Save as</button>
<button id="fake-editor-close" type="button">Close</button>
<script>
  // The postMessage channel as Collabora opens it (decision 87): the editor says it is Initialized, waits for the host's
  // Host_PostmessageReady, and then says the document is loaded. Save As asks the host for the copy's name (UI_SaveAs); the
  // host answers Action_SaveAs with the name, and the editor makes the copy through the host (the fake server does that).
  const post = (msg) => window.parent.postMessage(JSON.stringify(msg), '*');
  window.addEventListener('message', (ev) => {
    if (ev.source !== window.parent) return;
    const msg = typeof ev.data === 'string' ? JSON.parse(ev.data) : ev.data;
    if (msg.MessageId === 'Host_PostmessageReady') post({ MessageId: 'App_LoadingStatus', Values: { Status: 'Document_Loaded' } });
    if (msg.MessageId === 'Action_SaveAs') {
      fetch('/fake/save-as', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ filename: msg.Values.Filename }) });
    }
  });
  document.getElementById('fake-editor-saveas').addEventListener('click', () => post({ MessageId: 'UI_SaveAs', Values: {} }));
  // The run asks for Save As through the fake (requestSaveAs): the page polls for that, and then acts as the button does, since
  // the run cannot click inside this frame, which is another origin.
  setInterval(() => {
    fetch('/fake/command', { cache: 'no-store' }).then((r) => r.json()).then((c) => {
      if (c.command === 'save-as') post({ MessageId: 'UI_SaveAs', Values: {} });
    }).catch(() => {});
  }, 200);
  document.getElementById('fake-editor-close').addEventListener('click', () => post({ MessageId: 'UI_Close', Values: {} }));
  post({ MessageId: 'App_LoadingStatus', Values: { Status: 'Initialized' } });
</script></body></html>`;
}

// startConvertFake serves the fake on port. It returns the address, the certificate file the server must trust, the hits it
// received, and refuse(on) to make every conversion fail.
export async function startConvertFake(dir, port, { delayMs = 600 } = {}) {
  const keyFile = join(dir, 'convert-fake-key.pem');
  const certFile = join(dir, 'convert-fake-cert.pem');
  await execFileP('openssl', [
    'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', keyFile, '-out', certFile, '-days', '2',
    '-subj', '/CN=127.0.0.1', '-addext', 'subjectAltName=IP:127.0.0.1',
  ]);
  const { publicKey, privateKey } = generateKeyPairSync('rsa', { modulusLength: 2048 });
  const jwk = publicKey.export({ format: 'jwk' });
  const origin = `https://127.0.0.1:${port}`;
  const discovery = `<?xml version="1.0" encoding="utf-8"?>
<wopi-discovery>
    <net-zone name="external-https">
        <app name="writer">
            <action default="true" ext="docx" name="edit" urlsrc="${origin}/browser/42745e44dd/cool.html?"/>
            <action default="true" ext="odt" name="edit" urlsrc="${origin}/browser/42745e44dd/cool.html?"/>
        </app>
        <app name="calc">
            <action default="true" ext="xlsx" name="edit" urlsrc="${origin}/browser/42745e44dd/cool.html?"/>
            <action default="true" ext="ods" name="edit" urlsrc="${origin}/browser/42745e44dd/cool.html?"/>
        </app>
        <app name="impress">
            <action default="true" ext="pptx" name="edit" urlsrc="${origin}/browser/42745e44dd/cool.html?"/>
            <action default="true" ext="odp" name="edit" urlsrc="${origin}/browser/42745e44dd/cool.html?"/>
        </app>
        <app name="Capabilities">
            <action ext="" name="getinfo" urlsrc="${origin}/hosting/capabilities"/>
        </app>
    </net-zone>
    <proof-key value="" oldvalue="" modulus="${b64(jwk.n)}" exponent="${b64(jwk.e)}" oldmodulus="" oldexponent=""/>
</wopi-discovery>
`;
  const capabilities = JSON.stringify({
    'convert-to': { available: true, endpoint: '/cool/convert-to' },
    productVersion: '26.04.5.2',
    productVersionHash: '42745e44dd',
  });
  const hits = [];
  // editor is what each opening of the editor page saw: the file, whether it may write, whether it took the lock.
  const editor = [];
  // saves is each Save As the fake made: the name the person chose, and the host's status and the copy's name.
  const saves = [];
  // lastEditor is the token and WOPI address of the editor the fake opened last; Save As posts through them.
  let lastEditor = null;
  // pendingCommand is what the open editor page is asked to do next (GET /fake/command takes it).
  let pendingCommand = null;
  let refusing = false;
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

  const server = createServer({ key: readFileSync(keyFile), cert: readFileSync(certFile) }, async (req, res) => {
    const url = new URL(req.url, origin);
    if (req.method === 'GET' && url.pathname === '/hosting/discovery') {
      res.writeHead(200, { 'Content-Type': 'text/xml' });
      res.end(discovery);
      return;
    }
    if (req.method === 'GET' && url.pathname === '/hosting/capabilities') {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(capabilities);
      return;
    }
    if (req.method === 'POST' && url.pathname === EDITOR_PATH) {
      const chunks = [];
      for await (const c of req) chunks.push(c);
      const form = new URLSearchParams(Buffer.concat(chunks).toString('utf8'));
      const token = form.get('access_token') ?? '';
      const wopiSrc = url.searchParams.get('WOPISrc') ?? '';
      const seen = await openAsEditor(privateKey, token, wopiSrc);
      editor.push({ ...seen, actionHadToken: url.search.includes('access_token') });
      lastEditor = { token, wopiSrc };
      res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' });
      res.end(editorPage(seen));
      return;
    }
    if (req.method === 'GET' && url.pathname === '/fake/command') {
      const command = pendingCommand;
      pendingCommand = null;
      res.writeHead(200, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' });
      res.end(JSON.stringify({ command }));
      return;
    }
    if (req.method === 'POST' && url.pathname === '/fake/save-as') {
      const chunks = [];
      for await (const c of req) chunks.push(c);
      let filename = '';
      try {
        filename = String(JSON.parse(Buffer.concat(chunks).toString('utf8')).filename ?? '');
      } catch {
        filename = '';
      }
      if (lastEditor === null || filename === '') {
        res.writeHead(400);
        res.end();
        return;
      }
      const answer = await saveAsAsEditor(privateKey, lastEditor, filename);
      saves.push({ filename, ...answer });
      res.writeHead(200, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' });
      res.end(JSON.stringify(answer));
      return;
    }
    const m = /^\/cool\/convert-to\/([a-z0-9]{1,8})$/.exec(url.pathname);
    if (req.method !== 'POST' || !m || !FORMATS.includes(m[1])) {
      res.writeHead(404);
      res.end();
      return;
    }
    const chunks = [];
    for await (const c of req) chunks.push(c);
    const body = Buffer.concat(chunks).toString('latin1');
    const name = /filename="source\.([a-z0-9]{1,8})"/i.exec(body)?.[1]?.toLowerCase() ?? '';
    const format = m[1];
    hits.push({ format, source: name });
    await sleep(delayMs);
    const cross = format !== 'pdf' && format !== 'png' && FAMILY[name] !== FAMILY[format];
    if (refusing || cross) {
      res.writeHead(500, { 'X-Error-Kind': 'savefailed', 'X-Content-Type-Options': 'nosniff' });
      res.end();
      return;
    }
    let out;
    if (format === 'pdf') out = minimalPDF();
    else if (format === 'png') out = Buffer.from('89504e470d0a1a0a', 'hex');
    else out = Buffer.concat([Buffer.from('504b0304', 'hex'), Buffer.alloc(120, 0)]);
    res.writeHead(200, {
      'Content-Type': 'application/octet-stream',
      'Content-Disposition': `attachment; filename="source.${format}"`,
      'X-Content-Type-Options': 'nosniff',
      'Content-Length': out.length,
    });
    res.end(out);
  });
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(port, '127.0.0.1', resolve);
  });
  return {
    origin,
    certFile,
    hits,
    editor,
    saves,
    // requestSaveAs asks the open editor page to press Save as (the dialog then asks the person for the copy's name).
    requestSaveAs() {
      pendingCommand = 'save-as';
    },
    refuse(on) {
      refusing = on;
    },
    close: () => new Promise((resolve) => server.close(() => resolve())),
  };
}

// Exported for the run, which names the discovery address in the server's configuration.
export const DISCOVERY_PATH = '/hosting/discovery';

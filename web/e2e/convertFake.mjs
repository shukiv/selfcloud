// SPDX-License-Identifier: AGPL-3.0-or-later

// A fake Collabora conversion interface for the browser run (slice s4b). It answers the way decision 37 records the live
// server does: discovery lists the edit actions on its own host and no convert-to; /hosting/capabilities says conversion is
// available; POST /cool/convert-to/<format> takes a multipart part named source.<ext> and answers the converted bytes, with
// the type application/octet-stream and nosniff. A conversion between two families (writer, calc, impress) that is not PDF
// is answered 500 with X-Error-Kind: savefailed, as the live server answers a document it cannot save. The run can refuse
// every conversion with refuse(true), to show the failed state. It serves HTTPS on 127.0.0.1 with a certificate made here;
// the server under test trusts it through SSL_CERT_FILE, which the run sets.
import { execFile } from 'node:child_process';
import { generateKeyPairSync } from 'node:crypto';
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

// startConvertFake serves the fake on port. It returns the address, the certificate file the server must trust, the hits it
// received, and refuse(on) to make every conversion fail.
export async function startConvertFake(dir, port, { delayMs = 600 } = {}) {
  const keyFile = join(dir, 'convert-fake-key.pem');
  const certFile = join(dir, 'convert-fake-cert.pem');
  await execFileP('openssl', [
    'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', keyFile, '-out', certFile, '-days', '2',
    '-subj', '/CN=127.0.0.1', '-addext', 'subjectAltName=IP:127.0.0.1',
  ]);
  const { publicKey } = generateKeyPairSync('rsa', { modulusLength: 2048 });
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
    refuse(on) {
      refusing = on;
    },
    close: () => new Promise((resolve) => server.close(() => resolve())),
  };
}

// Exported for the run, which names the discovery address in the server's configuration.
export const DISCOVERY_PATH = '/hosting/discovery';

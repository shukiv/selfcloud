// SPDX-License-Identifier: AGPL-3.0-or-later

package search

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/names"
	"github.com/shukiv/selfcloud/internal/office"
)

// TestLiveCollaboraTxt is the live run of decision 46: a document is converted to txt by a real Collabora Online through
// the conversion client, and its text comes back as the content worker reads it. It runs only when
// SELFCLOUD_LIVE_COLLABORA names the URL of a Collabora (its scheme, host and port) and
// SELFCLOUD_LIVE_COLLABORA_CA names the PEM file of its certificate, and skips otherwise, so no CI run reaches a converter.
// The certificate is pinned, not skipped: the connection is verified against that one certificate.
func TestLiveCollaboraTxt(t *testing.T) {
	url := os.Getenv("SELFCLOUD_LIVE_COLLABORA")
	caFile := os.Getenv("SELFCLOUD_LIVE_COLLABORA_CA")
	if url == "" || caFile == "" {
		t.Skip("SELFCLOUD_LIVE_COLLABORA and SELFCLOUD_LIVE_COLLABORA_CA are not both set: no live Collabora to run against")
	}
	pemBytes, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatal("SELFCLOUD_LIVE_COLLABORA_CA holds no PEM certificate")
	}
	pinned := block.Bytes
	// The container's certificate names only its Common Name, which Go does not accept as a host name, so the chain and
	// the host name are not checked. The peer's leaf must be exactly the pinned certificate instead, which is a stronger test
	// for one known container. Any other certificate fails the handshake.
	tlsConf := &tls.Config{
		InsecureSkipVerify: true, // #nosec G402 -- replaced by the pin below
		MinVersion:         tls.VersionTLS12,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 || !bytes.Equal(raw[0], pinned) {
				return errors.New("the certificate is not the pinned one")
			}
			return nil
		},
	}
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConf}}
	c, err := office.New(url, hc, office.Options{Concurrent: 1, Timeout: 90 * time.Second, MaxOutput: 500 << 20})
	if err != nil {
		t.Fatal(err)
	}
	caps, err := c.Capabilities(context.Background())
	if err != nil || !caps.Available {
		t.Fatalf("capabilities %+v, err %v: conversion is not available", caps, err)
	}
	t.Logf("Collabora %s (build %s)", caps.Version, caps.VersionHash)

	doc := liveDocx(t, []string{"שָׁלוֹם לכולם", "التقرير السنوي", "one ascii line"})
	tw := &textWriter{max: 1 << 20}
	err = c.Convert(context.Background(), bytes.NewReader(doc), int64(len(doc)), "docx", "txt", tw)
	if err != nil && !tw.more {
		t.Fatalf("convert: %v", err)
	}
	text, truncated, err := ReadText(bytes.NewReader(keepWhole(tw.buf)), 1<<20)
	if err != nil {
		t.Fatalf("the converter's text is not text: %v", err)
	}
	if truncated {
		t.Fatal("a three-line document was truncated")
	}
	got := string(text)
	t.Logf("converted text: %q", got)
	for _, want := range []string{"שָׁלוֹם לכולם", "التقرير السنوي", "one ascii line"} {
		if !strings.Contains(got, want) {
			t.Errorf("the converted text lacks %q", want)
		}
	}
	if strings.HasPrefix(got, "\xef\xbb\xbf") {
		t.Error("a byte order mark survived the reader")
	}
	// The worker's form of the text: folded, so the niqqud query finds it.
	if form := searchFormForTest(got); !strings.Contains(form, "שלום לכולם") {
		t.Errorf("search form %q lacks the folded Hebrew", form)
	}
}

// liveDocx builds a minimal WordprocessingML document with one paragraph per line.
func liveDocx(t *testing.T, lines []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
			`</Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
			`</Relationships>`,
	}
	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	for _, l := range lines {
		body.WriteString("<w:p><w:r><w:t xml:space=\"preserve\">" + escapeXML(l) + "</w:t></w:r></w:p>")
	}
	body.WriteString(`</w:body></w:document>`)
	files["word/document.xml"] = body.String()
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml"} {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// escapeXML escapes the characters XML reserves in text.
func escapeXML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// searchFormForTest is the search form of a text, as the worker stores it.
func searchFormForTest(s string) string {
	return names.SearchForm(s)
}

// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// SMTPConfig says how to reach the mail server.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string // "" for no authentication
	Password string
	TLS      string // starttls, tls or none
	From     string // a bare address or "Name <address>"
}

// dialTimeout and sendTimeout bound one message. net/smtp has no context, so the connection's
// deadline carries the limit, and the context's deadline wins when it is sooner.
const (
	dialTimeout = 30 * time.Second
	sendTimeout = 60 * time.Second
)

// SMTP sends mail through an SMTP server with net/smtp.
type SMTP struct {
	cfg  SMTPConfig
	from *netmail.Address
}

// NewSMTP checks cfg and returns a sender for it.
func NewSMTP(cfg SMTPConfig) (*SMTP, error) {
	if cfg.Host == "" {
		return nil, errors.New("mail: smtp host is empty")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("mail: smtp port %d is not a port number", cfg.Port)
	}
	switch cfg.TLS {
	case "starttls", "tls", "none":
	default:
		return nil, fmt.Errorf("mail: smtp tls %q is not starttls, tls or none", cfg.TLS)
	}
	from, err := netmail.ParseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("mail: from: %w", err)
	}
	if err := checkHeader("from", from.String()); err != nil {
		return nil, err
	}
	return &SMTP{cfg: cfg, from: from}, nil
}

// Send delivers m. An error may name the server's reply but never the message's content.
//
// With smtp_tls none, net/smtp refuses to send a password unless the host is localhost, so an
// account with a password needs starttls or tls.
func (s *SMTP) Send(ctx context.Context, m Message) error {
	to, err := netmail.ParseAddress(m.To)
	if err != nil {
		return fmt.Errorf("mail: recipient: %w", err)
	}
	msg, err := buildMessage(s.from, to, m.Subject, m.Body, time.Now())
	if err != nil {
		return err
	}
	c, err := s.dial(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if s.cfg.Username != "" {
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("mail: smtp authentication: %w", err)
		}
	}
	if err := c.Mail(s.from.Address); err != nil {
		return fmt.Errorf("mail: sender refused: %w", err)
	}
	if err := c.Rcpt(to.Address); err != nil {
		return fmt.Errorf("mail: recipient refused: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		w.Close()
		return fmt.Errorf("mail: writing message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: ending message: %w", err)
	}
	return c.Quit()
}

// dial connects, upgrades to TLS as configured and greets the server. The returned client is
// closed by the caller. Cancelling ctx closes the connection, so a stuck server cannot hold
// the send past its context.
func (s *SMTP) dial(ctx context.Context) (*smtp.Client, error) {
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	tlsCfg := &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
	nd := &net.Dialer{Timeout: dialTimeout}
	var conn net.Conn
	var err error
	if s.cfg.TLS == "tls" {
		conn, err = (&tls.Dialer{NetDialer: nd, Config: tlsCfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = nd.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("mail: connecting to smtp server: %w", err)
	}
	deadline := time.Now().Add(sendTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("mail: smtp greeting: %w", err)
	}
	context.AfterFunc(ctx, func() { conn.Close() })
	if s.cfg.TLS == "starttls" {
		if err := c.StartTLS(tlsCfg); err != nil {
			c.Close()
			return nil, fmt.Errorf("mail: starttls: %w", err)
		}
	}
	return c, nil
}

// buildMessage returns the message as it goes over the wire: headers, then a quoted-printable
// body with CRLF line endings. Header values are checked for line breaks first, so nothing a
// caller supplies can add a header.
func buildMessage(from, to *netmail.Address, subject, body string, now time.Time) ([]byte, error) {
	if err := checkHeader("subject", subject); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from.String())
	fmt.Fprintf(&b, "To: %s\r\n", to.Address)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&b, "Date: %s\r\n", now.UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	// Only CRLF leaves the sender: a bare CR or LF in a body is how SMTP smuggling starts.
	text := strings.ReplaceAll(CleanBody(body), "\n", "\r\n")
	if _, err := qp.Write([]byte(text)); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	b.WriteString("\r\n")
	return b.Bytes(), nil
}

// CleanBody normalises a body of user-written text: CRLF and lone CR become LF, and every control
// character but LF and TAB is dropped, NUL included. The sender turns the LFs into CRLF, and nothing
// else in a body may carry a line break or a NUL.
func CleanBody(s string) string {
	s = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s)
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// checkHeader refuses a header value with a line break or a NUL byte.
func checkHeader(name, v string) error {
	if strings.ContainsAny(v, "\r\n\x00") {
		return fmt.Errorf("mail: %s contains a line break", name)
	}
	return nil
}

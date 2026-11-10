// Package mail sends a member's login mail (M5, ADR-0040; api.md 10.2,
// tech-stack.md「寄信」): through Gmail's SMTP with an app password, or,
// while developing, into the log.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"net/textproto"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
)

// GmailAddr is Gmail's SMTP submission port (STARTTLS); GCE blocks 25 only.
const GmailAddr = "smtp.gmail.com:587"

// Sender delivers one login mail with link to to.
type Sender interface {
	Send(ctx context.Context, to, link string) error
}

// Setup is what serve reads from its environment: a Sender and the
// https://<host> (or a local http:// one for the log) the links point to.
type Setup struct {
	Sender  Sender
	BaseURL string
}

// FromEnv reads BETHERE_MAIL and the settings it needs (api.md 10.2
// 「寄信的設定」). Anything missing or off is no usable setup: the error
// says why, and serve only logs it; mail never stops the site.
func FromEnv(lookupEnv func(string) (string, bool), logger *slog.Logger) (Setup, error) {
	get := func(k string) string { v, _ := lookupEnv(k); return v }
	mode := get("BETHERE_MAIL")
	if setup, ok := setups[mode]; ok {
		return setup(get, logger)
	}
	if mode == "" {
		return Setup{}, errors.New("BETHERE_MAIL is not set; login mails are off")
	}
	return Setup{}, fmt.Errorf("BETHERE_MAIL=%q: only smtp or log is accepted; login mails are off", mode)
}

// setups are the accepted BETHERE_MAIL values.
var setups = map[string]func(get func(string) string, logger *slog.Logger) (Setup, error){
	"smtp": func(get func(string) string, _ *slog.Logger) (Setup, error) { return smtpSetup(get) },
	"log": func(get func(string) string, logger *slog.Logger) (Setup, error) {
		return logSetup(get("BETHERE_BASE_URL"), logger)
	},
}

func smtpSetup(get func(string) string) (Setup, error) {
	from, password, base := get("BETHERE_MAIL_FROM"), get("BETHERE_MAIL_PASSWORD"), get("BETHERE_BASE_URL")
	needs := errors.New("BETHERE_MAIL=smtp needs BETHERE_MAIL_FROM (one address) and BETHERE_MAIL_PASSWORD")
	for _, c := range []struct {
		bad bool
		err error
	}{
		{!domain.IsPlainEmail(from), needs},
		{password == "", needs},
		{hostURL(base, "https") == "", fmt.Errorf("BETHERE_BASE_URL=%q must be https://<host> for BETHERE_MAIL=smtp", base)},
	} {
		if c.bad {
			return Setup{}, c.err
		}
	}
	return Setup{Sender: SMTP{Addr: GmailAddr, From: from, Password: password}, BaseURL: base}, nil
}

func logSetup(base string, logger *slog.Logger) (Setup, error) {
	if h := hostURL(base, "http"); h != "localhost" && h != "127.0.0.1" {
		return Setup{}, fmt.Errorf("BETHERE_BASE_URL=%q must be http://localhost or http://127.0.0.1 for BETHERE_MAIL=log", base)
	}
	return Setup{Sender: Log{Logger: logger}, BaseURL: base}, nil
}

// hostURL is the host of s when s is exactly scheme://<host>[:port], with
// nothing else (no user, path, query or fragment); otherwise "".
func hostURL(s, scheme string) string {
	u, err := url.Parse(s)
	if err != nil || s != scheme+"://"+u.Host {
		return ""
	}
	return u.Hostname() // "" when there is no host
}

// Link is the login mail's link: the page reads the token from the
// fragment, which never reaches a server (US-CO-1.9).
func Link(baseURL, token string) string { return baseURL + "/login#t=" + token }

// message is the plain-text mail of US-CO-1.9; to and from are plain
// addresses (domain.IsPlainEmail), so no header can be added through them.
func message(from, to, link string) []byte {
	subject := mime.BEncoding.Encode("UTF-8", "BeThere 登入連結")
	body := strings.Join([]string{
		"請點下面的連結，再按「登入」：",
		"",
		link,
		"",
		"15 分鐘內有效，只能用一次。",
		"不是你要求的，請忽略這封信。",
	}, "\r\n")
	return []byte("From: " + from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n" +
		"\r\n" + body + "\r\n")
}

// SMTP sends through a submission server with STARTTLS and PLAIN auth.
type SMTP struct {
	Addr, From, Password string
	// TLS is nil for the server's own name; tests set their own.
	TLS *tls.Config
}

// Send delivers one mail. Ending ctx closes the connection, so a send in
// progress stops at once (api.md 10.2, shutdown).
func (m SMTP) Send(ctx context.Context, to, link string) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", m.Addr)
	if err != nil {
		return err
	}
	// Ending ctx, its deadline too, closes the connection.
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	host, _, _ := net.SplitHostPort(m.Addr)
	err = m.session(conn, host, to, link)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (m SMTP) session(conn net.Conn, host, to, link string) error {
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	return steps(
		func() error { return c.StartTLS(m.tlsConfig(host)) },
		func() error { return c.Auth(smtp.PlainAuth("", m.From, m.Password, host)) },
		func() error { return c.Mail(m.From) },
		func() error { return c.Rcpt(to) },
		func() error { return data(c, message(m.From, to, link)) },
		c.Quit,
	)
}

// steps runs each step until one fails.
func steps(fns ...func() error) error {
	for _, fn := range fns {
		if err := fn(); err != nil {
			return err
		}
	}
	return nil
}

func (m SMTP) tlsConfig(host string) *tls.Config {
	if m.TLS != nil {
		return m.TLS
	}
	return &tls.Config{ServerName: host}
}

// data sends the mail itself; the server's answer comes with Close.
func data(c *smtp.Client, msg []byte) error {
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	return w.Close()
}

// Log writes the mail into the service log instead (US-CO-1.14); FromEnv
// allows it only with a local base URL.
type Log struct{ Logger *slog.Logger }

func (l Log) Send(_ context.Context, to, link string) error {
	l.Logger.Info("mail (log only)", "to", to, "link", link)
	return nil
}

// Kind names a send's failure for the log without its text, which may
// carry the recipient's address (api.md 10.2): an SMTP reply's code and
// enhanced code, or timeout, connect, tls or canceled.
func Kind(err error) string {
	var te *textproto.Error
	if errors.As(err, &te) {
		return replyKind(te)
	}
	for _, k := range kinds {
		if k.is(err) {
			return k.name
		}
	}
	return "connect"
}

// kinds name the failures without an SMTP reply, tried in order.
var kinds = []struct {
	name string
	is   func(error) bool
}{
	{"canceled", func(err error) bool { return errors.Is(err, context.Canceled) }},
	{"timeout", isTimeout},
	{"tls", isTLS},
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout()
}

func isTLS(err error) bool {
	var re tls.RecordHeaderError
	var ae tls.AlertError
	var ce *tls.CertificateVerificationError
	return errors.As(err, &re) || errors.As(err, &ae) || errors.As(err, &ce)
}

// replyKind is an SMTP reply's code, with its enhanced code when the reply
// starts with one.
func replyKind(te *textproto.Error) string {
	if enhanced, _, _ := strings.Cut(te.Msg, " "); enhancedCode.MatchString(enhanced) {
		return fmt.Sprintf("%d %s", te.Code, enhanced)
	}
	return fmt.Sprint(te.Code)
}

// enhancedCode is an enhanced status code such as 5.7.8 (RFC 3463); a
// reply that starts with anything else, an address say, logs its code only.
var enhancedCode = regexp.MustCompile(`^[245]\.[0-9]{1,3}\.[0-9]{1,3}$`)

// Timeout bounds one send (api.md 10.2): thirty seconds.
const Timeout = 30 * time.Second

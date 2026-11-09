package mail

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"mime"
	"net"
	"net/textproto"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

// api.md 10.2「寄信的設定」: smtp needs a sender, a password and an https
// host; log needs a local http one; anything else is no setup.
func TestUS_CO_1_12_FromEnv(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	smtpOK := map[string]string{"BETHERE_MAIL": "smtp", "BETHERE_MAIL_FROM": "bethere@gmail.com",
		"BETHERE_MAIL_PASSWORD": "abcdabcdabcdabcd", "BETHERE_BASE_URL": "https://34-80-7-111.sslip.io"}
	s, err := FromEnv(env(smtpOK), logger)
	if err != nil || s.BaseURL != "https://34-80-7-111.sslip.io" ||
		s.Sender != (SMTP{Addr: "smtp.gmail.com:587", From: "bethere@gmail.com", Password: "abcdabcdabcdabcd"}) {
		t.Errorf("smtp = %+v %v", s, err)
	}
	for _, base := range []string{"http://localhost", "http://127.0.0.1:8080", "http://localhost:5173"} {
		s, err := FromEnv(env(map[string]string{"BETHERE_MAIL": "log", "BETHERE_BASE_URL": base}), logger)
		if _, isLog := s.Sender.(Log); err != nil || !isLog || s.BaseURL != base {
			t.Errorf("log %s = %+v %v", base, s, err)
		}
	}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range smtpOK {
			m[a] = b
		}
		m[k] = v
		return m
	}
	for name, m := range map[string]map[string]string{
		"unset":                  {},
		"other mode":             with("BETHERE_MAIL", "sendgrid"),
		"no sender":              with("BETHERE_MAIL_FROM", ""),
		"two senders":            with("BETHERE_MAIL_FROM", "a@x.tw,b@x.tw"),
		"no password":            with("BETHERE_MAIL_PASSWORD", ""),
		"no base":                with("BETHERE_BASE_URL", ""),
		"http base for smtp":     with("BETHERE_BASE_URL", "http://bethere.tw"),
		"path":                   with("BETHERE_BASE_URL", "https://bethere.tw/x"),
		"slash":                  with("BETHERE_BASE_URL", "https://bethere.tw/"),
		"query":                  with("BETHERE_BASE_URL", "https://bethere.tw?x"),
		"fragment":               with("BETHERE_BASE_URL", "https://bethere.tw#x"),
		"user":                   with("BETHERE_BASE_URL", "https://u@bethere.tw"),
		"no host":                with("BETHERE_BASE_URL", "https://"),
		"bad url":                with("BETHERE_BASE_URL", "https://%zz"),
		"upper-case scheme":      with("BETHERE_BASE_URL", "HTTPS://bethere.tw"),
		"port without a host":    with("BETHERE_BASE_URL", "https://:443"),
		"encoded host":           with("BETHERE_BASE_URL", "https://%C3%A9.tw"),
		"log upper-case scheme":  {"BETHERE_MAIL": "log", "BETHERE_BASE_URL": "HTTP://localhost"},
		"log on the real domain": {"BETHERE_MAIL": "log", "BETHERE_BASE_URL": "https://bethere.tw"},
		"log on another host":    {"BETHERE_MAIL": "log", "BETHERE_BASE_URL": "http://bethere.tw"},
		"log with a path":        {"BETHERE_MAIL": "log", "BETHERE_BASE_URL": "http://localhost/x"},
	} {
		if s, err := FromEnv(env(m), logger); err == nil || s.Sender != nil {
			t.Errorf("%s: %+v %v", name, s, err)
		}
	}
}

// US-CO-1.9: the link's token is in the fragment of /login.
func TestUS_CO_1_9_Link(t *testing.T) {
	if got := Link("https://bethere.tw", "abc"); got != "https://bethere.tw/login#t=abc" {
		t.Errorf("link = %q", got)
	}
}

// US-CO-1.9: a plain-text mail with the subject, the link and the two
// lines, and nothing about a company.
func TestUS_CO_1_9_Message(t *testing.T) {
	m := string(message("bethere@gmail.com", "a@x.tw", "https://bethere.tw/login#t=abc"))
	head, body, ok := strings.Cut(m, "\r\n\r\n")
	if !ok {
		t.Fatalf("no body: %q", m)
	}
	for _, h := range []string{"From: bethere@gmail.com", "To: a@x.tw", "MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8"} {
		if !strings.Contains(head, h+"\r\n") {
			t.Errorf("header %q missing: %q", h, head)
		}
	}
	_, subject, _ := strings.Cut(head, "Subject: ")
	subject, _, _ = strings.Cut(subject, "\r\n")
	if got, err := new(mime.WordDecoder).DecodeHeader(subject); err != nil || got != "BeThere 登入連結" {
		t.Errorf("subject = %q %v", got, err)
	}
	for _, line := range []string{"https://bethere.tw/login#t=abc", "15 分鐘內有效，只能用一次。", "不是你要求的，請忽略這封信。"} {
		if !strings.Contains(body, line+"\r\n") {
			t.Errorf("body lacks %q: %q", line, body)
		}
	}
}

// US-CO-1.14: the log sender writes the address and the link to the log.
func TestUS_CO_1_14_LogSender(t *testing.T) {
	var buf bytes.Buffer
	if err := (Log{Logger: slog.New(slog.NewJSONHandler(&buf, nil))}).Send(context.Background(), "a@x.tw", "http://localhost/login#t=abc"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"to":"a@x.tw"`) || !strings.Contains(buf.String(), `"link":"http://localhost/login#t=abc"`) {
		t.Errorf("log = %s", buf.String())
	}
}

// api.md 10.2: a failure's kind, never its text.
func TestUS_CO_1_12_Kind(t *testing.T) {
	timeout := &net.OpError{Op: "dial", Err: os.ErrDeadlineExceeded}
	for _, c := range []struct {
		err  error
		want string
	}{
		{&textproto.Error{Code: 535, Msg: "5.7.8 Username and Password not accepted"}, "535 5.7.8"},
		{fmt.Errorf("rcpt: %w", &textproto.Error{Code: 550, Msg: "5.1.1 <a@x.tw> does not exist"}), "550 5.1.1"},
		{&textproto.Error{Code: 421, Msg: "Service not available"}, "421"},
		{&textproto.Error{Code: 535, Msg: "5.7.8"}, "535 5.7.8"},
		{&textproto.Error{Code: 554, Msg: "6.0.0 x"}, "554"},
		{&textproto.Error{Code: 554, Msg: "5.0 x"}, "554"},
		{&textproto.Error{Code: 554, Msg: "5.a.0 x"}, "554"},
		{&textproto.Error{Code: 554, Msg: "5..0 x"}, "554"},
		{&textproto.Error{Code: 554, Msg: "5.1234.0 x"}, "554"},
		{&textproto.Error{Code: 554, Msg: "5.1.1234 x"}, "554"},
		{&textproto.Error{Code: 554, Msg: "5.123.123 x"}, "554 5.123.123"},
		{&textproto.Error{Code: 550, Msg: "a@x.tw rejected"}, "550"},
		{&textproto.Error{Code: 554, Msg: "4.4.2 x"}, "554 4.4.2"},
		{&textproto.Error{Code: 250, Msg: "2.0.0 x"}, "250 2.0.0"},
		{context.Canceled, "canceled"},
		{fmt.Errorf("send: %w", context.DeadlineExceeded), "timeout"},
		{timeout, "timeout"},
		{tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}, "tls"},
		{fmt.Errorf("x: %w", tls.AlertError(40)), "tls"},
		{&tls.CertificateVerificationError{Err: errors.New("unknown authority")}, "tls"},
		{&net.OpError{Op: "dial", Err: errors.New("connection refused")}, "connect"},
		{errors.New("anything to a@x.tw"), "connect"},
	} {
		if got := Kind(c.err); got != c.want {
			t.Errorf("Kind(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

// fakeSMTP is a submission server with STARTTLS and PLAIN auth that keeps
// what it is sent.
type fakeSMTP struct {
	ln       net.Listener
	cert     tls.Certificate
	password string
	rcptErr  string // a reply to RCPT, when set
	dataErr  string // a reply after DATA, when set
	hang     bool   // never greets
	mu       sync.Mutex
	got      []string // DATA, as received
	greeted  chan struct{}
}

// newFakeSMTP starts the fake; set changes it before it serves anyone.
func newFakeSMTP(t *testing.T, set ...func(*fakeSMTP)) (*fakeSMTP, *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, cert: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, password: "pw", greeted: make(chan struct{}, 1)}
	for _, s := range set {
		s(f)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f, &tls.Config{RootCAs: pool, ServerName: "127.0.0.1"}
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	if f.hang {
		f.greeted <- struct{}{}
		buf := make([]byte, 1)
		c.Read(buf) // until the client closes
		return
	}
	tp := textproto.NewConn(c)
	tp.PrintfLine("220 fake ESMTP")
	tlsOn := false
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.Fields(line + " x")[0])
		switch {
		case cmd == "EHLO" && !tlsOn:
			tp.PrintfLine("250-fake")
			tp.PrintfLine("250 STARTTLS")
		case cmd == "EHLO":
			tp.PrintfLine("250-fake")
			tp.PrintfLine("250 AUTH PLAIN")
		case cmd == "STARTTLS":
			tp.PrintfLine("220 go ahead")
			tc := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{f.cert}})
			if tc.Handshake() != nil {
				return
			}
			c, tp, tlsOn = tc, textproto.NewConn(tc), true
		case cmd == "AUTH":
			raw, _ := base64.StdEncoding.DecodeString(strings.Fields(line)[2])
			if string(raw) != "\x00bethere@gmail.com\x00"+f.password {
				tp.PrintfLine("535 5.7.8 Username and Password not accepted")
				continue
			}
			tp.PrintfLine("235 2.7.0 Accepted")
		case cmd == "MAIL":
			tp.PrintfLine("250 ok")
		case cmd == "RCPT" && f.rcptErr != "":
			tp.PrintfLine("%s", f.rcptErr)
		case cmd == "RCPT":
			tp.PrintfLine("250 ok")
		case cmd == "DATA":
			tp.PrintfLine("354 go")
			data, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			if f.dataErr != "" {
				tp.PrintfLine("%s", f.dataErr)
				continue
			}
			f.mu.Lock()
			f.got = append(f.got, string(data))
			f.mu.Unlock()
			tp.PrintfLine("250 queued")
		case cmd == "QUIT":
			tp.PrintfLine("221 bye")
			return
		default:
			tp.PrintfLine("502 no")
		}
	}
}

// The mail goes through STARTTLS and PLAIN auth to the recipient.
func TestUS_CO_1_9_SMTPSends(t *testing.T) {
	f, cfg := newFakeSMTP(t)
	m := SMTP{Addr: f.ln.Addr().String(), From: "bethere@gmail.com", Password: "pw", TLS: cfg}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.Send(ctx, "a@x.tw", "https://bethere.tw/login#t=abc"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.got) != 1 || !strings.Contains(f.got[0], "To: a@x.tw\n") || !strings.Contains(f.got[0], "https://bethere.tw/login#t=abc") {
		t.Errorf("received = %q", f.got)
	}
}

// Each refusal is the server's reply, with its code.
func TestUS_CO_1_12_SMTPRefusals(t *testing.T) {
	ctx := context.Background()
	f, cfg := newFakeSMTP(t)
	m := SMTP{Addr: f.ln.Addr().String(), From: "bethere@gmail.com", Password: "wrong", TLS: cfg}
	if err := m.Send(ctx, "a@x.tw", "l"); Kind(err) != "535 5.7.8" {
		t.Errorf("wrong password: %v", err)
	}
	m.Password = "pw"
	f, cfg = newFakeSMTP(t, func(f *fakeSMTP) { f.rcptErr = "550 5.1.1 <a@x.tw> does not exist" })
	m.Addr, m.TLS = f.ln.Addr().String(), cfg
	if err := m.Send(ctx, "a@x.tw", "l"); Kind(err) != "550 5.1.1" {
		t.Errorf("refused recipient: %v", err)
	}
	f, cfg = newFakeSMTP(t, func(f *fakeSMTP) { f.dataErr = "554 5.7.0 message refused" })
	m.Addr, m.TLS = f.ln.Addr().String(), cfg
	if err := m.Send(ctx, "a@x.tw", "l"); Kind(err) != "554 5.7.0" {
		t.Errorf("refused message: %v", err)
	}
	m.TLS = &tls.Config{ServerName: "127.0.0.1"} // the fake's certificate is not trusted
	if err := m.Send(ctx, "a@x.tw", "l"); Kind(err) != "tls" {
		t.Errorf("untrusted certificate: %v", err)
	}
	m.Addr = "127.0.0.1:1"
	if err := m.Send(ctx, "a@x.tw", "l"); Kind(err) != "connect" {
		t.Errorf("nothing listening: %v", err)
	}
}

// Ending the context stops a send in progress at once (api.md 10.2).
func TestUS_CO_1_12_SMTPStopsWithTheContext(t *testing.T) {
	f, cfg := newFakeSMTP(t, func(f *fakeSMTP) { f.hang = true })
	m := SMTP{Addr: f.ln.Addr().String(), From: "bethere@gmail.com", Password: "pw", TLS: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Send(ctx, "a@x.tw", "l") }()
	<-f.greeted
	cancel()
	select {
	case err := <-done:
		if Kind(err) != "canceled" {
			t.Errorf("err = %v, want canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the send did not stop")
	}
	tctx, tcancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer tcancel()
	if err := m.Send(tctx, "a@x.tw", "l"); Kind(err) != "timeout" {
		t.Errorf("deadline: err = %v, want timeout", err)
	}
}

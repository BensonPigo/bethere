// Command bethere runs the BeThere server and its operator subcommands
// (api.md section 7 item 7).
//
//	bethere serve          -db <path> [-addr host:port]
//	bethere create-event   -db <path> -name <name> -date YYYY-MM-DD -tz <zone> -master-name <name> -base-url https://<host>
//	bethere add-role       -db <path> -name <name> -type Stylist|Host [-view HandsFree|Glance] -base-url https://<host>
//	bethere import-rundown -db <path> -file rundown.csv
//	bethere create-invite  -db <path>
//	bethere reset-recovery-code -db <path>
//	bethere issue-admin-link -db <path> -base-url https://<host>
//	bethere version
//	bethere features
//
// Every subcommand needs -db; there is no default, so a command can never
// open a database other than the one named. Only serve with BETHERE_INIT=1
// creates a database (tech-stack.md 2.1「不自動建庫」; api.md section 7). Links and recovery codes are printed to
// standard output only, never to a log (api.md section 7). add-role and import-rundown
// act as the Master: they ask for the Master link on the terminal without
// echo and never take it from arguments or the environment (api.md section 7).
// reset-recovery-code asks the same way for the Master link or the current
// recovery code (US-O-4).
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/BensonPigo/bethere/internal/api"
	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/loginmail"
	"github.com/BensonPigo/bethere/internal/mail"
	"github.com/BensonPigo/bethere/internal/rundown"
	"github.com/BensonPigo/bethere/internal/server"
	"github.com/BensonPigo/bethere/internal/store"
	"github.com/BensonPigo/bethere/web"
)

// version is set at build time: -ldflags "-X main.version=..."
var version = "dev"

const usage = "usage: bethere <serve|create-event|add-role|import-rundown|create-invite|reset-recovery-code|issue-admin-link|find-member|add-member|remove-member|issue-login-link|version|features> -db <path> ..."

// cli is the process environment a subcommand may use. Tests replace it.
type cli struct {
	stdin     io.Reader
	stdout    io.Writer
	stderr    io.Writer
	lookupEnv func(string) (string, bool)
}

func main() {
	os.Exit(run(os.Args[1:], cli{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, lookupEnv: os.LookupEnv}))
}

// errUsage marks a mistake in the command line (exit status 2).
var errUsage = errors.New("usage")

// commands are the subcommands, by name.
var commands = map[string]func([]string, cli) error{
	"version": func(_ []string, c cli) error {
		fmt.Fprintln(c.stdout, version)
		return nil
	},
	// The VM's start script asks before starting in maintenance mode, so
	// a release without it is refused rather than left open (US-O-2.1);
	// the release scripts and the start script refuse a release without
	// mail-login, an email-only one (US-CO-1.13, tech-stack.md「寄信」).
	"features": func(_ []string, c cli) error {
		fmt.Fprintln(c.stdout, "maintenance")
		fmt.Fprintln(c.stdout, "mail-login")
		return nil
	},
	"serve":               serve,
	"create-event":        createEvent,
	"add-role":            addRole,
	"import-rundown":      importRundown,
	"create-invite":       createInvite,
	"reset-recovery-code": resetRecoveryCode,
	"issue-admin-link":    issueAdminLink,
	"find-member":         findMember,
	"add-member":          addMember,
	"remove-member":       removeMember,
	"issue-login-link":    issueLoginLink,
}

func run(args []string, c cli) int {
	if len(args) < 1 {
		fmt.Fprintln(c.stderr, usage)
		return 2
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(c.stderr, "unknown command %q\n%s\n", args[0], usage)
		return 2
	}
	return exitCode(args[0], cmd(args[1:], c), c)
}

// exitCode reports a subcommand's error: 2 for a mistake in the command
// line, 1 for anything else.
func exitCode(name string, err error, c cli) int {
	if err == nil {
		return 0
	}
	fmt.Fprintf(c.stderr, "bethere %s: %v\n", name, err)
	if errors.Is(err, errUsage) {
		return 2
	}
	return 1
}

// parseFlags parses fs and refuses positional arguments, so nothing such
// as a link can be passed (and kept in shell history) by mistake.
func parseFlags(fs *flag.FlagSet, args []string, c cli) error {
	fs.SetOutput(c.stderr)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: unexpected argument %q (the Master link is asked for on the terminal)", errUsage, fs.Arg(0))
	}
	return nil
}

func required(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: -%s is required", errUsage, name)
	}
	return nil
}

// initFromEnv reads BETHERE_INIT (api.md section 7): "1" creates a new database, unset
// or empty opens an existing one, and any other value refuses to start,
// so values such as "0" or "true" cannot be mistaken for either.
func initFromEnv(lookupEnv func(string) (string, bool)) (bool, error) {
	v, _ := lookupEnv("BETHERE_INIT")
	switch v {
	case "":
		return false, nil
	case "1":
		return true, nil
	}
	return false, fmt.Errorf("BETHERE_INIT=%q: only 1 (create a new database on the very first deployment) or unset is accepted", v)
}

// maintenanceFromEnv reads BETHERE_MAINTENANCE (US-O-2.1): "1" is
// maintenance mode, "0" or unset is open, and anything else refuses to
// start. The VM's unit sets "unread" until its start script has read the
// flag from metadata, so a flag that was never read never means open.
func maintenanceFromEnv(lookupEnv func(string) (string, bool)) (bool, error) {
	v, _ := lookupEnv("BETHERE_MAINTENANCE")
	switch v {
	case "", "0":
		return false, nil
	case "1":
		return true, nil
	}
	return false, fmt.Errorf("BETHERE_MAINTENANCE=%q: only 1 (maintenance mode) or 0 is accepted", v)
}

// supportEmailFromEnv reads BETHERE_SUPPORT_EMAIL (US-O-8.2): unset or
// empty shows no address; a value that is not one plain address is dropped
// with an error, which serve only logs: a support line never stops the site.
func supportEmailFromEnv(lookupEnv func(string) (string, bool)) (string, error) {
	v, _ := lookupEnv("BETHERE_SUPPORT_EMAIL")
	if v == "" || domain.IsPlainEmail(v) {
		return v, nil
	}
	return "", fmt.Errorf("BETHERE_SUPPORT_EMAIL=%q is not one email address; no support address is shown", v)
}

// serveOptions is what serve reads from its flags and environment.
type serveOptions struct {
	addr, dbPath        string
	create, maintenance bool
}

func readServeOptions(args []string, c cli) (serveOptions, error) {
	var o serveOptions
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.StringVar(&o.addr, "addr", "127.0.0.1:8080", "listen address (Caddy proxies to it)")
	fs.StringVar(&o.dbPath, "db", "", "database file (required)")
	if err := parseFlags(fs, args, c); err != nil {
		return o, err
	}
	if err := required("db", o.dbPath); err != nil {
		return o, err
	}
	var err error
	if o.create, err = initFromEnv(c.lookupEnv); err != nil {
		return o, err
	}
	o.maintenance, err = maintenanceFromEnv(c.lookupEnv)
	return o, err
}

func serve(args []string, c cli) error {
	opt, err := readServeOptions(args, c)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := store.Open(ctx, opt.dbPath, opt.create)
	if err != nil {
		return err
	}
	defer st.Close()

	logger := slog.New(slog.NewJSONHandler(c.stdout, nil))
	// Before any request: after a restore no member link of the backup
	// works (US-O-12.5).
	resetAfterRestore(ctx, st, opt.dbPath, logger)
	support := supportEmail(c, logger)
	queue, mailed := startLoginMail(ctx, st, c, logger)
	defer func() {
		stop()
		<-mailed // the worker ends, then its last log line, before the store closes
	}()
	srv := &http.Server{
		Addr: opt.addr,
		Handler: server.New(server.Config{
			Static:       web.Dist(),
			Store:        st,
			Now:          time.Now, // the system clock only (api.md section 1)
			Version:      version,
			Logger:       logger,
			Maintenance:  opt.maintenance,
			SupportEmail: support,
			LoginMail:    queue,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second, // above the store's 5 s busy timeout
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	purged := startPurge(ctx, st, opt.maintenance, logger)
	defer func() {
		stop()
		<-purged
	}()

	logger.Info("listening", "addr", opt.addr, "version", version, "maintenance", opt.maintenance)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// supportEmail is the support address to show, or "" with the error
// logged: a support line never stops the site (US-O-8.2).
func supportEmail(c cli, logger *slog.Logger) string {
	support, err := supportEmailFromEnv(c.lookupEnv)
	if err != nil {
		logger.Error("support email", "err", err)
	}
	return support
}

// startLoginMail starts the login mail queue's worker when the
// environment has a usable mail setup (api.md 10.2); otherwise it logs why
// and returns a nil queue, so the login answers mail_unavailable: mail
// never stops the site. The channel closes once the worker has stopped.
func startLoginMail(ctx context.Context, st *store.Store, c cli, logger *slog.Logger) (api.Enqueuer, <-chan struct{}) {
	done := make(chan struct{})
	setup, err := mail.FromEnv(c.lookupEnv, logger)
	if err != nil {
		logger.Warn("mail setup", "err", err)
		close(done)
		return nil, done
	}
	q := loginmail.New(loginmail.Config{Store: st, Sender: setup.Sender, BaseURL: setup.BaseURL,
		Now: time.Now, Logger: logger, Every: time.Minute})
	go func() {
		defer close(done)
		q.Run(ctx)
	}()
	return q, done
}

// startPurge runs the daily cleanup (US-O-5) until ctx ends; the channel
// closes when it has stopped, so serve stops it before the store closes.
// It waits out maintenance mode: nothing is deleted while the Master
// checks a restored database.
func startPurge(ctx context.Context, st *store.Store, maintenance bool, logger *slog.Logger) <-chan struct{} {
	purged := make(chan struct{})
	go func() {
		defer close(purged)
		if maintenance {
			logger.Info("purge paused: maintenance mode")
			return
		}
		purgeDaily(ctx, st.PurgeEnded, time.Now, 24*time.Hour, logger)
	}()
	return purged
}

// openExisting opens the database named by -db, never creating one (api.md section 7).
func openExisting(ctx context.Context, path string) (*store.Store, error) {
	return store.Open(ctx, path, false)
}

func createEvent(args []string, c cli) error {
	fs := flag.NewFlagSet("create-event", flag.ContinueOnError)
	dbPath := fs.String("db", "", "database file (required)")
	name := fs.String("name", "", "event name (required)")
	date := fs.String("date", "", "event date, YYYY-MM-DD (required)")
	tz := fs.String("tz", "", "event time zone, e.g. Asia/Taipei (required)")
	masterName := fs.String("master-name", "", "name of the Master role (required)")
	baseURL := fs.String("base-url", "", "https://<host> the links point to (required)")
	if err := parseFlags(fs, args, c); err != nil {
		return err
	}
	for _, f := range []struct{ name, value string }{
		{"db", *dbPath}, {"name", *name}, {"date", *date}, {"tz", *tz}, {"master-name", *masterName}, {"base-url", *baseURL},
	} {
		if err := required(f.name, f.value); err != nil {
			return err
		}
	}
	base, err := parseBaseURL(*baseURL)
	if err != nil {
		return err
	}
	day, err := time.Parse("2006-01-02", *date)
	if err != nil {
		return fmt.Errorf("%w: -date %q is not YYYY-MM-DD", errUsage, *date)
	}

	ctx := context.Background()
	st, err := openExisting(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	token, err := auth.NewToken()
	if err != nil {
		return err
	}
	code, err := auth.NewRecoveryCode()
	if err != nil {
		return err
	}
	codeHash, err := auth.HashRecoveryCode(code)
	if err != nil {
		return err
	}
	eventID, err := st.CreateEvent(ctx, store.NewEvent{
		Name:             *name,
		Date:             domain.Date{Year: day.Year(), Month: day.Month(), Day: day.Day()},
		TimeZone:         *tz,
		MasterName:       *masterName,
		MasterTokenHash:  auth.HashToken(token),
		RecoveryCodeHash: codeHash,
	}, time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "Created event %d (%s, %s, %s), status Draft.\n", eventID, *name, *date, *tz)
	fmt.Fprintf(c.stdout, "Master link (shown only this once):\n  %s\n", link(base, token))
	fmt.Fprintf(c.stdout, "Recovery code (shown only this once; keep it apart from the link, e.g. on paper):\n  %s\n", code)
	return nil
}

// createInvite makes an invite code for /new (US-M-17.1): one event, within
// store.InviteLife. The code is printed once with its number, the one the
// event's create-event log names; only its hash is stored.
func createInvite(args []string, c cli) error {
	fs := flag.NewFlagSet("create-invite", flag.ContinueOnError)
	dbPath := fs.String("db", "", "database file (required)")
	if err := parseFlags(fs, args, c); err != nil {
		return err
	}
	if err := required("db", *dbPath); err != nil {
		return err
	}
	ctx := context.Background()
	st, err := openExisting(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	code, err := auth.NewRecoveryCode()
	if err != nil {
		return err
	}
	codeHash, err := auth.HashRecoveryCode(code)
	if err != nil {
		return err
	}
	now := time.Now()
	id, err := st.CreateInvite(ctx, codeHash, now)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "Invite %d, for one event, until %s.\n", id, now.Add(store.InviteLife).UTC().Format(time.RFC3339))
	fmt.Fprintf(c.stdout, "Invite code (shown only this once; the event is created at /new):\n  %s\n", code)
	return nil
}

func addRole(args []string, c cli) error {
	fs := flag.NewFlagSet("add-role", flag.ContinueOnError)
	dbPath := fs.String("db", "", "database file (required)")
	name := fs.String("name", "", "role name, e.g. 新秘 (required)")
	typ := fs.String("type", "", "role type: Stylist, Media, StageAV or Host (required)")
	view := fs.String("view", "", "screen: HandsFree or Glance; default by type (ADR-0025)")
	baseURL := fs.String("base-url", "", "https://<host> the link points to (required)")
	if err := parseFlags(fs, args, c); err != nil {
		return err
	}
	for _, f := range []struct{ name, value string }{{"db", *dbPath}, {"name", *name}, {"type", *typ}, {"base-url", *baseURL}} {
		if err := required(f.name, f.value); err != nil {
			return err
		}
	}
	// P0 opens Stylist, Media, StageAV and Host; P1 types cannot be
	// created (api.md section 4).
	roleType := domain.RoleType(*typ)
	if !roleType.HasOwnCues() && roleType != domain.Host {
		return fmt.Errorf("%w: -type %q: only Stylist, Media, StageAV or Host", errUsage, *typ)
	}
	roleView := domain.View(*view)
	if roleView != "" && !roleType.AllowsView(roleView) {
		return fmt.Errorf("%w: -view %q does not suit -type %s", errUsage, *view, roleType)
	}
	base, err := parseBaseURL(*baseURL)
	if err != nil {
		return err
	}

	ctx := context.Background()
	st, err := openExisting(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	master, err := askMaster(ctx, st, c)
	if err != nil {
		return err
	}
	roleID, err := st.AddRoleWithView(ctx, master.EventID, *name, roleType, roleView, master.Actor, time.Now())
	if err != nil {
		return err
	}
	token, err := auth.NewToken()
	if err != nil {
		return err
	}
	if err := st.IssueToken(ctx, roleID, auth.HashToken(token), master.Actor, time.Now()); err != nil {
		return fmt.Errorf("role %d was added but its link was not issued: %w", roleID, err)
	}
	if roleView == "" {
		roleView = roleType.DefaultView()
	}
	fmt.Fprintf(c.stdout, "Added role %d: %s (%s, %s).\n", roleID, *name, roleType, roleView)
	fmt.Fprintf(c.stdout, "Link (shown only this once):\n  %s\n", link(base, token))
	return nil
}

func importRundown(args []string, c cli) error {
	fs := flag.NewFlagSet("import-rundown", flag.ContinueOnError)
	dbPath := fs.String("db", "", "database file (required)")
	file := fs.String("file", "", "rundown CSV file (required)")
	if err := parseFlags(fs, args, c); err != nil {
		return err
	}
	for _, f := range []struct{ name, value string }{{"db", *dbPath}, {"file", *file}} {
		if err := required(f.name, f.value); err != nil {
			return err
		}
	}
	f, err := os.Open(*file)
	if err != nil {
		return err
	}
	rows, err := rundown.Parse(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("%s: %w; nothing was imported", *file, err)
	}

	ctx := context.Background()
	st, err := openExisting(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	master, err := askMaster(ctx, st, c)
	if err != nil {
		return err
	}
	if err := st.ImportRundown(ctx, master.EventID, rows, master.Actor, time.Now()); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "Imported %d cues.\n", len(rows))
	return nil
}

// resetRecoveryCode gives an event a new recovery code (US-O-4). The
// event is named by its Master link or by its current recovery code, read
// like the Master link in askMaster: the code is what is left when a
// leaked copy has taken the Master link over. The new code is printed
// once, before it is stored; if printing fails nothing changes, and if
// storing fails the printed code is declared void (US-O-4.4).
func resetRecoveryCode(args []string, c cli) error {
	fs := flag.NewFlagSet("reset-recovery-code", flag.ContinueOnError)
	dbPath := fs.String("db", "", "database file (required)")
	if err := parseFlags(fs, args, c); err != nil {
		return err
	}
	if err := required("db", *dbPath); err != nil {
		return err
	}
	ctx := context.Background()
	st, err := openExisting(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	fmt.Fprint(c.stderr, "Master link or current recovery code (input hidden): ")
	line, err := readSecretLine(c.stdin)
	fmt.Fprintln(c.stderr)
	if err != nil {
		return fmt.Errorf("reading the input: %w", err)
	}
	var actor store.Actor
	var eventID int64
	if token, err := tokenFromLink(line); err == nil {
		owner, err := proveMaster(ctx, st, token)
		if err != nil {
			return err
		}
		actor, eventID = owner.Actor, owner.EventID
	} else if oldHash, err := auth.HashRecoveryCode(line); err == nil {
		actor, eventID, err = st.LookupRecoveryCode(ctx, oldHash)
		if errors.Is(err, store.ErrInvalidRecoveryCode) {
			return errors.New("the recovery code is wrong or was reset already")
		}
		if err != nil {
			return err
		}
	} else {
		return errors.New("not a Master link or recovery code")
	}

	code, err := auth.NewRecoveryCode()
	if err != nil {
		return err
	}
	codeHash, err := auth.HashRecoveryCode(code)
	if err != nil {
		return err
	}
	// Printed before it is stored: a code nobody saw must never replace the
	// one in hand, which may be the only way back in (RISK-19).
	if _, err := fmt.Fprintf(c.stdout, "New recovery code for event %d (shown only this once; give it only to the backup operator, apart from the link):\n  %s\n",
		eventID, code); err != nil {
		return fmt.Errorf("the new code could not be shown (%v); nothing was changed", err)
	}
	switch err := st.ResetRecoveryCode(ctx, actor, codeHash, time.Now()); {
	case errors.Is(err, domain.ErrEnded):
		return errors.New("the event has ended; nothing was changed and the code above does not work")
	case errors.Is(err, store.ErrInvalidRecoveryCode), errors.Is(err, store.ErrInvalidToken):
		return errors.New("the link or code stopped working before the reset; nothing was changed and the code above does not work")
	case err != nil:
		return fmt.Errorf("%w; nothing was changed and the code above does not work", err)
	}
	fmt.Fprintln(c.stdout, "Reset: the old recovery code no longer works.")
	return nil
}

// issueAdminLink makes the admin page's link (US-O-9.1, ADR-0038) and
// prints it once; the last one stops working. It asks for nothing: being
// able to run it on the server is what makes the developer.
func issueAdminLink(args []string, c cli) error {
	fs := flag.NewFlagSet("issue-admin-link", flag.ContinueOnError)
	dbPath := fs.String("db", "", "database file (required)")
	baseURL := fs.String("base-url", "", "https://<host> the link points to (required)")
	if err := parseFlags(fs, args, c); err != nil {
		return err
	}
	for _, f := range []struct{ name, value string }{{"db", *dbPath}, {"base-url", *baseURL}} {
		if err := required(f.name, f.value); err != nil {
			return err
		}
	}
	base, err := parseBaseURL(*baseURL)
	if err != nil {
		return err
	}
	ctx := context.Background()
	st, err := openExisting(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	token, err := auth.NewToken()
	if err != nil {
		return err
	}
	if err := st.IssueAdminLink(ctx, auth.HashToken(token)); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "Admin link (shown only this once; the last one no longer works):\n  %s/admin#t=%s\n", base, token)
	return nil
}

// askMaster asks for the Master link on the terminal without echo, reads
// it from standard input only, and proves it with the store (api.md section 7). The
// store does not check role types, so being the Master is checked here.
func askMaster(ctx context.Context, st *store.Store, c cli) (store.TokenOwner, error) {
	fmt.Fprint(c.stderr, "Master link (input hidden): ")
	line, err := readSecretLine(c.stdin)
	fmt.Fprintln(c.stderr)
	if err != nil {
		return store.TokenOwner{}, fmt.Errorf("reading the Master link: %w", err)
	}
	token, err := tokenFromLink(line)
	if err != nil {
		return store.TokenOwner{}, err
	}
	return proveMaster(ctx, st, token)
}

// proveMaster looks the token up and accepts only a valid Master link: a
// member's console link is no link here (US-CO-4.6).
func proveMaster(ctx context.Context, st *store.Store, token string) (store.TokenOwner, error) {
	owner, err := st.LookupRoleToken(ctx, auth.HashToken(token), time.Now())
	if err != nil {
		return store.TokenOwner{}, masterLinkError(err)
	}
	if owner.RoleType != domain.Master {
		return store.TokenOwner{}, errors.New("this is not the Master link")
	}
	return owner, nil
}

// masterLinkError says why a Master link was not taken.
func masterLinkError(err error) error {
	switch {
	case errors.Is(err, store.ErrInvalidToken):
		return errors.New("the Master link is invalid or was replaced")
	case errors.Is(err, store.ErrEventEnded):
		return errors.New("the event of this link has ended")
	}
	return err
}

// readSecretLine reads one line: from the terminal without echo, or from
// a pipe as is.
func readSecretLine(r io.Reader) (string, error) {
	if f, ok := r.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		b, err := term.ReadPassword(int(f.Fd()))
		return string(b), err
	}
	line, err := bufio.NewReader(io.LimitReader(r, 4096)).ReadString('\n')
	if errors.Is(err, io.EOF) && line != "" {
		err = nil
	}
	return line, err
}

// tokenFromLink accepts a whole link (https://host/#t=<token>) or the bare
// token, and checks the token's format.
func tokenFromLink(s string) (string, error) {
	s = strings.TrimSpace(s)
	token := s
	if i := strings.LastIndexByte(s, '#'); i >= 0 {
		frag, ok := strings.CutPrefix(s[i+1:], "t=")
		if !ok {
			return "", errors.New("the link has no #t=<token>")
		}
		token = frag
	}
	if auth.CheckToken(token) != nil {
		return "", errors.New("not a BeThere link or token")
	}
	return token, nil
}

// parseBaseURL accepts https://<host>[:port] with nothing else, and returns
// it without a trailing slash.
func parseBaseURL(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(s, "#") {
		return "", fmt.Errorf("%w: -base-url %q must be https://<host> with no path, query or fragment", errUsage, s)
	}
	return "https://" + u.Host, nil
}

// link is the role link of access-control.md 3.2.
func link(base, token string) string { return base + "/#t=" + token }

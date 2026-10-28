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
	"regexp"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/rundown"
	"github.com/BensonPigo/bethere/internal/server"
	"github.com/BensonPigo/bethere/internal/store"
	"github.com/BensonPigo/bethere/web"
)

// version is set at build time: -ldflags "-X main.version=..."
var version = "dev"

const usage = "usage: bethere <serve|create-event|add-role|import-rundown|create-invite|reset-recovery-code|issue-admin-link|version|features> -db <path> ..."

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

func run(args []string, c cli) int {
	if len(args) < 1 {
		fmt.Fprintln(c.stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "serve":
		err = serve(args[1:], c)
	case "create-event":
		err = createEvent(args[1:], c)
	case "add-role":
		err = addRole(args[1:], c)
	case "import-rundown":
		err = importRundown(args[1:], c)
	case "create-invite":
		err = createInvite(args[1:], c)
	case "reset-recovery-code":
		err = resetRecoveryCode(args[1:], c)
	case "issue-admin-link":
		err = issueAdminLink(args[1:], c)
	case "version":
		fmt.Fprintln(c.stdout, version)
		return 0
	case "features":
		// The VM's start script asks before starting in maintenance mode, so
		// a release without it is refused rather than left open (US-O-2.1).
		fmt.Fprintln(c.stdout, "maintenance")
		return 0
	default:
		fmt.Fprintf(c.stderr, "unknown command %q\n%s\n", args[0], usage)
		return 2
	}
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errUsage):
		fmt.Fprintf(c.stderr, "bethere %s: %v\n", args[0], err)
		return 2
	default:
		fmt.Fprintf(c.stderr, "bethere %s: %v\n", args[0], err)
		return 1
	}
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

// supportAddress is one plain address: nothing that could be read as
// another scheme, a display name or a second recipient (US-O-8.2).
var supportAddress = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}$`)

// supportEmailFromEnv reads BETHERE_SUPPORT_EMAIL (US-O-8.2): unset or
// empty shows no address; a value that is not one plain address is dropped
// with an error, which serve only logs: a support line never stops the site.
func supportEmailFromEnv(lookupEnv func(string) (string, bool)) (string, error) {
	v, _ := lookupEnv("BETHERE_SUPPORT_EMAIL")
	if v == "" || supportAddress.MatchString(v) {
		return v, nil
	}
	return "", fmt.Errorf("BETHERE_SUPPORT_EMAIL=%q is not one email address; no support address is shown", v)
}

func serve(args []string, c cli) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8080", "listen address (Caddy proxies to it)")
	dbPath := fs.String("db", "", "database file (required)")
	if err := parseFlags(fs, args, c); err != nil {
		return err
	}
	if err := required("db", *dbPath); err != nil {
		return err
	}
	create, err := initFromEnv(c.lookupEnv)
	if err != nil {
		return err
	}
	maintenance, err := maintenanceFromEnv(c.lookupEnv)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := store.Open(ctx, *dbPath, create)
	if err != nil {
		return err
	}
	defer st.Close()

	logger := slog.New(slog.NewJSONHandler(c.stdout, nil))
	support, err := supportEmailFromEnv(c.lookupEnv)
	if err != nil {
		logger.Error("support email", "err", err)
	}
	srv := &http.Server{
		Addr: *addr,
		Handler: server.New(server.Config{
			Static:       web.Dist(),
			Store:        st,
			Now:          time.Now, // the system clock only (api.md section 1)
			Version:      version,
			Logger:       logger,
			Maintenance:  maintenance,
			SupportEmail: support,
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

	// The daily cleanup (US-O-5) stops with the server, before the store
	// closes. It waits out maintenance mode: nothing is deleted while the
	// Master checks a restored database.
	purged := make(chan struct{})
	go func() {
		defer close(purged)
		if maintenance {
			logger.Info("purge paused: maintenance mode")
			return
		}
		purgeDaily(ctx, st.PurgeEnded, time.Now, 24*time.Hour, logger)
	}()
	defer func() {
		stop()
		<-purged
	}()

	logger.Info("listening", "addr", *addr, "version", version, "maintenance", maintenance)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
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

// proveMaster looks the token up and accepts only a valid Master link.
func proveMaster(ctx context.Context, st *store.Store, token string) (store.TokenOwner, error) {
	owner, err := st.LookupToken(ctx, auth.HashToken(token), time.Now())
	switch {
	case errors.Is(err, store.ErrInvalidToken):
		return store.TokenOwner{}, errors.New("the Master link is invalid or was replaced")
	case errors.Is(err, store.ErrEventEnded):
		return store.TokenOwner{}, errors.New("the event of this link has ended")
	case err != nil:
		return store.TokenOwner{}, err
	}
	if owner.RoleType != domain.Master {
		return store.TokenOwner{}, errors.New("this is not the Master link")
	}
	return owner, nil
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

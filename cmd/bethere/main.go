// Command bethere runs the BeThere server and its operator subcommands
// (api.md section 7 item 7).
//
//	bethere serve          -db <path> [-addr host:port]
//	bethere create-event   -db <path> -name <name> -date YYYY-MM-DD -tz <zone> -master-name <name> -base-url https://<host>
//	bethere add-role       -db <path> -name <name> -type Stylist -base-url https://<host>
//	bethere import-rundown -db <path> -file rundown.csv
//	bethere version
//
// Every subcommand needs -db; there is no default, so a command can never
// open a database other than the one named. Only serve with BETHERE_INIT=1
// creates a database (tech-stack.md 2.1「不自動建庫」; api.md section 7). Links and recovery codes are printed to
// standard output only, never to a log (api.md section 7). add-role and import-rundown
// act as the Master: they ask for the Master link on the terminal without
// echo and never take it from arguments or the environment (api.md section 7).
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

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/rundown"
	"github.com/BensonPigo/bethere/internal/server"
	"github.com/BensonPigo/bethere/internal/store"
	"github.com/BensonPigo/bethere/web"
)

// version is set at build time: -ldflags "-X main.version=..."
var version = "dev"

const usage = "usage: bethere <serve|create-event|add-role|import-rundown|version> -db <path> ..."

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
	case "version":
		fmt.Fprintln(c.stdout, version)
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := store.Open(ctx, *dbPath, create)
	if err != nil {
		return err
	}
	defer st.Close()

	logger := slog.New(slog.NewJSONHandler(c.stdout, nil))
	srv := &http.Server{
		Addr: *addr,
		Handler: server.New(server.Config{
			Static:  web.Dist(),
			Store:   st,
			Now:     time.Now, // the system clock only (api.md section 1)
			Version: version,
			Logger:  logger,
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

	logger.Info("listening", "addr", *addr, "version", version)
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

func addRole(args []string, c cli) error {
	fs := flag.NewFlagSet("add-role", flag.ContinueOnError)
	dbPath := fs.String("db", "", "database file (required)")
	name := fs.String("name", "", "role name, e.g. 新秘 (required)")
	typ := fs.String("type", "", "role type; M1 accepts only Stylist (required)")
	baseURL := fs.String("base-url", "", "https://<host> the link points to (required)")
	if err := parseFlags(fs, args, c); err != nil {
		return err
	}
	for _, f := range []struct{ name, value string }{{"db", *dbPath}, {"name", *name}, {"type", *typ}, {"base-url", *baseURL}} {
		if err := required(f.name, f.value); err != nil {
			return err
		}
	}
	// M1 opens only Stylist; Media, StageAV and Host follow in M2, and P1
	// types cannot be created in P0 (api.md section 4).
	if domain.RoleType(*typ) != domain.Stylist {
		return fmt.Errorf("%w: -type %q: M1 accepts only Stylist", errUsage, *typ)
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
	roleID, err := st.AddRole(ctx, master.EventID, *name, domain.Stylist, master.Actor, time.Now())
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
	fmt.Fprintf(c.stdout, "Added role %d: %s (Stylist).\n", roleID, *name)
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

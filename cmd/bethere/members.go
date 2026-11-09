package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/BensonPigo/bethere/internal/auth"
	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/store"
)

// memberResetMarker is the restore's "member links not yet cleared" mark,
// next to the database (api.md 10.2): the start script makes it before a
// restore, and only a successful reset removes it.
func memberResetMarker(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "member-reset.pending")
}

// resetAfterRestore clears every member's login and console link when the
// marker is there (US-O-12.5). Any failure is logged with the prefix
// member-reset and closes member links for this run (US-O-12.6); it never
// stops serve, so a wedding is never kept down by a company's links.
func resetAfterRestore(ctx context.Context, st *store.Store, dbPath string, logger *slog.Logger) {
	marker := memberResetMarker(dbPath)
	if _, err := os.Stat(marker); errors.Is(err, fs.ErrNotExist) {
		return
	} else if err != nil {
		closeMembers(st, logger, "reading the marker", err)
		return
	}
	if err := st.ResetMemberAccess(ctx); err != nil {
		closeMembers(st, logger, "clearing the links", err)
		return
	}
	if err := os.Remove(marker); err != nil {
		closeMembers(st, logger, "removing the marker", err)
		return
	}
	logger.Info("member-reset: every member's login and console link was cleared after the restore")
}

func closeMembers(st *store.Store, logger *slog.Logger, step string, err error) {
	st.CloseMemberAccess()
	logger.Error("member-reset: "+step+" failed; member links stay closed until a start clears them", "err", err)
}

// memberFlags parses -db and -email, the email cleaned as the company page
// does (US-O-11.2).
func memberFlags(name string, args []string, c cli, extra func(*flag.FlagSet)) (dbPath, email string, err error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	db := fs.String("db", "", "database file (required)")
	raw := fs.String("email", "", "the member's email (required)")
	if extra != nil {
		extra(fs)
	}
	if err := parseFlags(fs, args, c); err != nil {
		return "", "", err
	}
	for _, f := range []struct{ name, value string }{{"db", *db}, {"email", *raw}} {
		if err := required(f.name, f.value); err != nil {
			return "", "", err
		}
	}
	email, ok := domain.CleanEmail(*raw)
	if !ok {
		return "", "", fmt.Errorf("%w: -email %q is not one plain email address", errUsage, *raw)
	}
	return *db, email, nil
}

// findMember prints the company of an email (US-O-12.1).
func findMember(args []string, c cli) error {
	dbPath, email, err := memberFlags("find-member", args, c, nil)
	if err != nil {
		return err
	}
	ctx := context.Background()
	st, err := openExisting(ctx, dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	id, name, err := st.FindMember(ctx, email)
	if errors.Is(err, store.ErrNotMember) {
		fmt.Fprintf(c.stdout, "%s is a member of no company.\n", email)
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "%s is a member of company %d (%s).\n", email, id, name)
	return nil
}

// addMember adds an email to a company (US-O-12.2).
func addMember(args []string, c cli) error {
	var org int64
	dbPath, email, err := memberFlags("add-member", args, c, func(fs *flag.FlagSet) {
		fs.Int64Var(&org, "org", 0, "the company's number (required)")
	})
	if err != nil {
		return err
	}
	if org < 1 {
		return fmt.Errorf("%w: -org is required", errUsage)
	}
	ctx := context.Background()
	st, err := openExisting(ctx, dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	switch err := st.AddMemberTo(ctx, org, email, time.Now()); {
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("there is no company %d", org)
	case errors.Is(err, store.ErrEmailTaken):
		return fmt.Errorf("%s is a member already; find-member names the company", email)
	case errors.Is(err, store.ErrTooManyMembers):
		return fmt.Errorf("company %d has %d members already", org, store.MembersPerOrganization)
	case err != nil:
		return err
	}
	fmt.Fprintf(c.stdout, "Added %s to company %d.\n", email, org)
	return nil
}

// removeMember removes the member with an email, a company's last too;
// their links stop working (US-O-12.3).
func removeMember(args []string, c cli) error {
	dbPath, email, err := memberFlags("remove-member", args, c, nil)
	if err != nil {
		return err
	}
	ctx := context.Background()
	st, err := openExisting(ctx, dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	id, name, err := st.RemoveMemberByEmail(ctx, email)
	if errors.Is(err, store.ErrNotMember) {
		return fmt.Errorf("%s is a member of no company", email)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "Removed %s from company %d (%s); their links no longer work.\n", email, id, name)
	return nil
}

// issueLoginLink prints a new login link for a member, for when login
// mails do not arrive (US-O-12.8); the developer hands it over.
func issueLoginLink(args []string, c cli) error {
	var rawBase string
	dbPath, email, err := memberFlags("issue-login-link", args, c, func(fs *flag.FlagSet) {
		fs.StringVar(&rawBase, "base-url", "", "https://<host> the link points to (required)")
	})
	if err != nil {
		return err
	}
	if err := required("base-url", rawBase); err != nil {
		return err
	}
	base, err := parseBaseURL(rawBase)
	if err != nil {
		return err
	}
	ctx := context.Background()
	st, err := openExisting(ctx, dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	token, err := auth.NewToken()
	if err != nil {
		return err
	}
	switch err := st.IssueLoginLink(ctx, email, auth.HashToken(token), time.Now()); {
	case errors.Is(err, store.ErrNotMember):
		return fmt.Errorf("%s is a member of no company; no link was made", email)
	case err != nil:
		return err
	}
	fmt.Fprintf(c.stdout, "Login link for %s (shown only this once; it works for 30 days):\n  %s/company#t=%s\n", email, base, token)
	return nil
}

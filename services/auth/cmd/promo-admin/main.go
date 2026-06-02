// promo-admin is a CLI for managing promo codes directly against the auth DB.
// Usage:
//
//	promo-admin create -code FREEMONTH [-email user@example.com] [-max-uses 100] [-max-per-user 1] [-notes "..."]
//	promo-admin list
//	promo-admin disable -code FREEMONTH
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"

	pkgpgx "github.com/fsociety/todo/pkg/pgx"
	"github.com/fsociety/todo/services/auth/internal/domain"
	"github.com/fsociety/todo/services/auth/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fatalf("DATABASE_URL must be set\n")
	}

	ctx := context.Background()
	pool, err := pkgpgx.New(ctx, pkgpgx.Config{DSN: dsn, MaxConns: 3, MinConns: 1}, slog.Default())
	if err != nil {
		fatalf("connect: %v\n", err)
	}
	defer pool.Close()

	s := store.New(pool)

	switch os.Args[1] {
	case "create":
		cmdCreate(ctx, s)
	case "list":
		cmdList(ctx, s)
	case "disable":
		cmdDisable(ctx, s)
	default:
		usage()
		os.Exit(1)
	}
}

func cmdCreate(ctx context.Context, s *store.Store) {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	code := fs.String("code", "", "promo code (required, stored uppercased)")
	email := fs.String("email", "", "restrict to this email (optional)")
	maxUses := fs.Int("max-uses", 0, "global max redemptions (0 = unlimited)")
	maxPerUser := fs.Int("max-per-user", 1, "max redemptions per user")
	notes := fs.String("notes", "", "internal notes")
	_ = fs.Parse(os.Args[2:])

	if *code == "" {
		fatalf("create: -code is required\n")
	}

	pc := &domain.PromoCode{
		ID:              uuid.New().String(),
		Code:            *code,
		MaxUsagePerUser: *maxPerUser,
		CreatedAt:       time.Now(),
	}
	if *email != "" {
		pc.AllowedEmail = email
	}
	if *maxUses > 0 {
		pc.MaxUsageCount = maxUses
	}
	if *notes != "" {
		pc.Notes = *notes
	}

	if err := s.CreatePromoCode(ctx, pc); err != nil {
		fatalf("create: %v\n", err)
	}
	fmt.Printf("created promo code %s (id=%s)\n", pc.Code, pc.ID)
}

func cmdList(ctx context.Context, s *store.Store) {
	codes, err := s.ListPromoCodes(ctx)
	if err != nil {
		fatalf("list: %v\n", err)
	}

	// Fetch all redemption counts in a single query to avoid N+1.
	redemptions, err := s.CountAllPromoCodeRedemptions(ctx)
	if err != nil {
		fatalf("list: count redemptions: %v\n", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CODE\tEMAIL\tMAX_USES\tPER_USER\tUSED\tDISABLED\tNOTES")
	for _, pc := range codes {
		email := "-"
		if pc.AllowedEmail != nil {
			email = *pc.AllowedEmail
		}
		maxUses := "∞"
		if pc.MaxUsageCount != nil {
			maxUses = fmt.Sprintf("%d", *pc.MaxUsageCount)
		}
		notes := pc.Notes
		if notes == "" {
			notes = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%v\t%s\n",
			pc.Code, email, maxUses, pc.MaxUsagePerUser, redemptions[pc.ID], pc.Disabled, notes)
	}
	w.Flush()
}

func cmdDisable(ctx context.Context, s *store.Store) {
	fs := flag.NewFlagSet("disable", flag.ExitOnError)
	code := fs.String("code", "", "promo code to disable (required)")
	_ = fs.Parse(os.Args[2:])

	if *code == "" {
		fatalf("disable: -code is required\n")
	}
	if err := s.DisablePromoCode(ctx, *code); err != nil {
		fatalf("disable: %v\n", err)
	}
	fmt.Printf("disabled promo code %s\n", *code)
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: promo-admin <command> [flags]

Commands:
  create   -code CODE [-email EMAIL] [-max-uses N] [-max-per-user N] [-notes TEXT]
  list
  disable  -code CODE

Environment:
  DATABASE_URL   Postgres connection string (required)`)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format, args...)
	os.Exit(1)
}

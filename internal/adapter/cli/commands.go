package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/grintzdel/spool/internal/bootstrap"
	"github.com/grintzdel/spool/internal/domain/signature"
	"github.com/grintzdel/spool/internal/usecase"
)

func newListCommand(g *globals) *cobra.Command {
	var (
		f      usecase.ListFilter
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List captured requests, most recent first",
		Args:    cobra.NoArgs,
		RunE: withApp(g, func(cmd *cobra.Command, _ []string, app *bootstrap.App) error {
			items, err := app.Queries.List(cmd.Context(), f)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return writeListJSON(out, items)
			}
			if len(items) == 0 {
				fmt.Fprintln(out, "No requests yet. Run `spool serve` and point a webhook sender at it.")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tRECEIVED\tMETHOD\tPATH\tPROVIDER\tEVENT\tVERIFY\tAPP")
			for _, it := range items {
				r := it.Request
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.ReceivedAt.Local().Format(timeLayout),
					r.Method, truncate(r.Path, 40), orDash(r.Provider), orDash(r.EventType),
					verifyStatus(it.Verification), appStatus(it.LastAttempt))
			}
			return tw.Flush()
		}),
	}
	cmd.Flags().StringVar(&f.Provider, "provider", "", "only this provider (stripe, github, ...)")
	cmd.Flags().StringVar(&f.EventType, "event", "", "only this event type")
	cmd.Flags().BoolVar(&f.FailedOnly, "failed", false, "only requests whose last delivery or verification failed")
	cmd.Flags().IntVar(&f.Limit, "limit", 20, "maximum number of requests")
	cmd.Flags().StringVar(&f.BeforeID, "before", "", "only requests older than this id (pagination)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return cmd
}

type listItemJSON struct {
	ID           string `json:"id"`
	ReceivedAt   string `json:"received_at"`
	Method       string `json:"method"`
	Path         string `json:"path"`
	Query        string `json:"query,omitempty"`
	Provider     string `json:"provider,omitempty"`
	EventType    string `json:"event_type,omitempty"`
	BodySHA256   string `json:"body_sha256"`
	Verification string `json:"verification,omitempty"`
	LastStatus   int    `json:"last_status,omitempty"`
	LastError    string `json:"last_error,omitempty"`
}

func writeListJSON(w interface{ Write([]byte) (int, error) }, items []usecase.RequestSummary) error {
	out := make([]listItemJSON, 0, len(items))
	for _, it := range items {
		r := it.Request
		j := listItemJSON{ID: r.ID, ReceivedAt: r.ReceivedAt.UTC().Format(time.RFC3339Nano), Method: r.Method,
			Path: r.Path, Query: r.Query, Provider: r.Provider, EventType: r.EventType, BodySHA256: r.BodySHA256}
		if it.Verification != nil {
			j.Verification = it.Verification.Reason
		}
		if it.LastAttempt != nil {
			j.LastStatus, j.LastError = it.LastAttempt.StatusCode, it.LastAttempt.Error
		}
		out = append(out, j)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func newShowCommand(g *globals) *cobra.Command {
	var raw, headersOnly, hex, asJSON bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a request, its delivery attempts and its diagnosis",
		Args:  cobra.ExactArgs(1),
		RunE: withApp(g, func(cmd *cobra.Command, args []string, app *bootstrap.App) error {
			d, err := app.Queries.Show(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			out, r := cmd.OutOrStdout(), d.Request
			switch {
			case raw:
				_, err := out.Write(r.Body)
				return err
			case asJSON:
				s, err := usecase.Export(r, usecase.FormatJSON, "")
				if err != nil {
					return err
				}
				_, err = fmt.Fprint(out, s)
				return err
			case hex:
				_, err := fmt.Fprint(out, hexDump(r.Body))
				return err
			}

			if !headersOnly {
				fmt.Fprintf(out, "%s %s", r.Method, r.Path)
				if r.Query != "" {
					fmt.Fprintf(out, "?%s", r.Query)
				}
				fmt.Fprintf(out, "\n  id:       %s\n  received: %s\n  provider: %s   event: %s\n  body:     %d bytes, sha256 %s\n",
					r.ID, r.ReceivedAt.Local().Format(timeLayout+".000"), orDash(r.Provider), orDash(r.EventType),
					len(r.Body), r.BodySHA256)
				fmt.Fprintln(out, "\nHeaders")
			}
			for _, h := range r.Headers {
				fmt.Fprintf(out, "  %s: %s\n", h.Name, h.Value)
			}
			if headersOnly {
				return nil
			}
			fmt.Fprintf(out, "\nBody\n%s\n", prettyBody(r.Body))
			if v := d.Verification; v != nil {
				fmt.Fprintf(out, "\nLast verification (%s): %s  %s\n", v.CheckedAt.Local().Format(timeLayout), v.Reason,
					signature.Reason(v.Reason).Meaning())
			}
			if d.SeenDiff != nil {
				fmt.Fprintf(out, "\nBody as seen by the app\n%s", d.SeenDiff.Render())
			}
			if len(d.Attempts) > 0 {
				fmt.Fprintln(out, "\nAttempts")
				for _, a := range d.Attempts {
					writeAttempt(out, a)
				}
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "write the exact body bytes to stdout")
	cmd.Flags().BoolVar(&headersOnly, "headers", false, "only print headers")
	cmd.Flags().BoolVar(&hex, "hex", false, "hex dump of the body")
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return cmd
}

func newVerifyCommand(g *globals) *cobra.Command {
	var (
		in       usecase.VerifyInput
		bodyFile string
		asJSON   bool
	)
	cmd := &cobra.Command{
		Use:   "verify <id>",
		Short: "Diagnose why a webhook signature passes or fails",
		Long: "Recomputes the signature on the stored raw body and names the cause of a failure.\n" +
			"Secrets are read from $SPOOL_<PROVIDER>_SECRET, --secret-env or .spool.toml, never from arguments.\n" +
			"Exits with status 1 when the verdict is not OK_RAW.",
		Args: cobra.ExactArgs(1),
		RunE: withApp(g, func(cmd *cobra.Command, args []string, app *bootstrap.App) error {
			in.ID = args[0]
			if bodyFile != "" {
				b, err := os.ReadFile(bodyFile)
				if err != nil {
					return err
				}
				in.AppBody = b
			}
			rep, err := app.Verifier.Verify(cmd.Context(), in)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(reportJSON(rep)); err != nil {
					return err
				}
			} else {
				writeReport(cmd.OutOrStdout(), rep)
			}
			if rep.Reason() != signature.OKRaw {
				return exitError{status: 1}
			}
			return nil
		}),
	}
	cmd.Flags().StringVar(&in.Provider, "provider", "", "force the provider instead of detecting it")
	cmd.Flags().StringVar(&in.SecretEnv, "secret-env", "", "name of the environment variable holding the secret")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "file with the body as your app saw it, to classify the difference")
	cmd.Flags().Var(durationFlag{&in.Tolerance}, "tolerance", "timestamp tolerance (default from config, 300s)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return cmd
}

func reportJSON(rep usecase.Report) map[string]any {
	out := map[string]any{
		"request_id": rep.Request.ID, "provider": rep.Provider, "ok": rep.Result.OK,
		"reason": rep.Result.Reason, "meaning": rep.Result.Reason.Meaning(), "hints": rep.Result.Hints,
		"expected": rep.Result.Expected, "got": rep.Result.Got,
	}
	if rep.Diff != nil {
		out["diff"] = map[string]any{"offset": rep.Diff.Offset, "raw": rep.Diff.RawContext, "app": rep.Diff.SeenContext}
		out["fixes"] = rep.Fixes
	}
	return out
}

func newReplayCommand(g *globals) *cobra.Command {
	var (
		in       usecase.ReplayInput
		headers  []string
		noMarker bool
	)
	cmd := &cobra.Command{
		Use:   "replay <id>",
		Short: "Send a stored request again",
		Args:  cobra.ExactArgs(1),
		RunE: withApp(g, func(cmd *cobra.Command, args []string, app *bootstrap.App) error {
			set, err := parseHeaderFlags(headers)
			if err != nil {
				return err
			}
			in.ID, in.SetHeaders, in.Marker = args[0], set, !noMarker
			res, err := app.Replayer.Replay(cmd.Context(), in)
			for _, w := range res.Warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w)
			}
			for _, a := range res.Attempts {
				writeAttempt(cmd.OutOrStdout(), a)
			}
			if err != nil {
				if errors.Is(err, usecase.ErrNoTarget) {
					return fmt.Errorf("%w (or set serve.forward in .spool.toml)", err)
				}
				return err
			}
			for _, a := range res.Attempts {
				if a.Failed() {
					return exitError{status: 1}
				}
			}
			return nil
		}),
	}
	cmd.Flags().StringVar(&in.To, "to", "", "target base URL (default: the configured forward target)")
	cmd.Flags().IntVar(&in.Times, "times", 1, "send the same request N times (idempotency testing)")
	cmd.Flags().BoolVar(&in.Resign, "resign", false, "recompute the signature with the current time and the known secret")
	cmd.Flags().StringVar(&in.Provider, "provider", "", "provider used by --resign (default: detected)")
	cmd.Flags().StringArrayVar(&headers, "set-header", nil, "override a header, K=V (repeatable)")
	cmd.Flags().BoolVar(&noMarker, "no-marker", false, "do not add the X-Spool-Replay header")
	return cmd
}

func newExportCommand(g *globals) *cobra.Command {
	var (
		format string
		to     string
	)
	cmd := &cobra.Command{
		Use:   "export <id>",
		Short: "Print a stored request as curl, JSON or raw HTTP",
		Args:  cobra.ExactArgs(1),
		RunE: withApp(g, func(cmd *cobra.Command, args []string, app *bootstrap.App) error {
			r, err := app.Store.FindRequest(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if to == "" {
				to = app.Config.Serve.Forward
			}
			s, err := usecase.Export(r, usecase.Format(format), to)
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), s)
			return err
		}),
	}
	cmd.Flags().StringVar(&format, "format", "curl", "curl, json or http")
	cmd.Flags().StringVar(&to, "to", "", "target base URL for curl (default: forward target, else the capture URL)")
	return cmd
}

func newPruneCommand(g *globals) *cobra.Command {
	var (
		olderThan time.Duration
		keep      int
	)
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete old requests from the history",
		Long:  "Without flags, applies the configured retention (30 days or 10,000 requests by default).",
		Args:  cobra.NoArgs,
		RunE: withApp(g, func(cmd *cobra.Command, _ []string, app *bootstrap.App) error {
			if !cmd.Flags().Changed("older-than") && !cmd.Flags().Changed("keep") {
				olderThan, keep = app.Config.Retention.MaxAge.D(), app.Config.Retention.MaxRequests
			}
			n, err := app.Pruner.Prune(cmd.Context(), olderThan, keep)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted %d request(s).\n", n)
			return nil
		}),
	}
	cmd.Flags().Var(durationFlag{&olderThan}, "older-than", "delete requests older than this (e.g. 7d, 12h)")
	cmd.Flags().IntVar(&keep, "keep", 0, "keep only the N most recent requests")
	return cmd
}

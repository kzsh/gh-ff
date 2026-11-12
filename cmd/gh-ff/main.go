package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"gh-from-file/opsfile"
	"gh-from-file/spec"
)

// Stamped by scripts/build.sh through -ldflags -X. A plain `go build` or
// `go install` leaves version at "dev" and commit and date empty.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

const usage = `usage:
  gh-ff run [-n] <ops.yaml>       execute a layer-1 ops file
  gh-ff compile <spec.yaml>       lower a layer-2 spec to ops YAML (stdout)
  gh-ff submit [-n] <spec.yaml>   compile a layer-2 spec and execute it
  gh-ff template <review|ops>     print a starter file for a format
  gh-ff sync <spec.yaml> [review-id]
      fetch server ids for an already-submitted review and write them into
      the file; discovers the review by author and summary when no id is
      in the file or given
  gh-ff --version                 print the version

  -n, --dry-run   print the gh commands without executing them
      --read-only abort unless every command is read-only
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gh-ff:", err)
		os.Exit(1)
	}
}

// Single line, so install.sh can read it whole.
func versionString() string {
	if commit == "" {
		return "gh-ff " + version
	}
	if date == "" {
		return "gh-ff " + version + " (" + commit + ")"
	}
	return "gh-ff " + version + " (" + commit + ", " + date + ")"
}

func run(args []string) error {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("expected a subcommand")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "version", "--version", "-v":
		fmt.Println(versionString())
		return nil
	case "run", "submit":
		fs := flag.NewFlagSet(sub, flag.ContinueOnError)
		dryRun := fs.Bool("dry-run", false, "print commands without executing")
		fs.BoolVar(dryRun, "n", *dryRun, "alias for --dry-run")
		readOnly := fs.Bool("read-only", false, "abort unless every command is read-only")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			fmt.Fprint(os.Stderr, usage)
			return fmt.Errorf("%s: expected exactly one file", sub)
		}
		if sub == "run" {
			f, err := opsfile.Load(fs.Arg(0))
			if err != nil {
				return err
			}
			if *readOnly {
				if err := f.AuditReadOnly(); err != nil {
					return err
				}
			}
			return execute(f, *dryRun)
		}
		d, err := spec.Load(fs.Arg(0))
		if err != nil {
			return err
		}
		f, err := d.Compile()
		if err != nil {
			return err
		}
		if *readOnly {
			if err := f.AuditReadOnly(); err != nil {
				return err
			}
		}
		if *dryRun {
			return execute(f, true)
		}
		if d.Review.ID != 0 {
			if err := execute(f, false); err != nil {
				return err
			}
			return publishIfPending(d.Review, d.Review.ID)
		}
		return submitNew(d.Review, f, fs.Arg(0))
	case "sync":
		if len(rest) < 1 || len(rest) > 2 {
			fmt.Fprint(os.Stderr, usage)
			return fmt.Errorf("sync: expected a spec file and an optional review id")
		}
		d, err := spec.Load(rest[0])
		if err != nil {
			return err
		}
		reviewID := d.Review.ID
		if len(rest) == 2 {
			if reviewID, err = strconv.ParseInt(rest[1], 10, 64); err != nil {
				return fmt.Errorf("sync: review id %q is not a number", rest[1])
			}
		} else if reviewID == 0 {
			if reviewID, err = discoverReview(d.Review); err != nil {
				return err
			}
		}
		if err := syncIDs(d.Review, rest[0], reviewID); err != nil {
			return err
		}
		fmt.Printf("ids for review %d written to %s\n", reviewID, rest[0])
		if err := syncReference(d.Review, rest[0]); err != nil {
			return err
		}
		fmt.Printf("pr %d comments written to %s under reference\n", d.Review.PR, rest[0])
		return nil
	case "template":
		if len(rest) == 1 {
			if tmpl, ok := templates[rest[0]]; ok {
				_, err := os.Stdout.WriteString(tmpl)
				return err
			}
		}
		return fmt.Errorf("template: expected one of: review, ops")
	case "compile":
		if len(rest) != 1 {
			fmt.Fprint(os.Stderr, usage)
			return fmt.Errorf("compile: expected exactly one spec file")
		}
		f, err := loadFor("submit", rest[0])
		if err != nil {
			return err
		}
		out, err := yaml.Marshal(f)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(out)
		return err
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown subcommand %q", sub)
	}
}

// loadFor returns the layer-1 ops for a path: parsed directly for "run",
// compiled from a layer-2 spec otherwise.
func loadFor(sub, path string) (*opsfile.File, error) {
	if sub == "run" {
		return opsfile.Load(path)
	}
	d, err := spec.Load(path)
	if err != nil {
		return nil, err
	}
	return d.Compile()
}

// submitNew creates a review and writes its ids into the spec file. A
// review with content is created pending, its ids recorded, and only then
// published (see spec.Review.HasContent), so a failure at any point leaves
// the file able to resume instead of duplicating.
func submitNew(r *spec.Review, f *opsfile.File, path string) error {
	tmpDir, err := os.MkdirTemp("", "gh-ff-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	op := f.Ops[0]
	if err := op.Materialize(tmpDir, "op0-"); err != nil {
		return err
	}
	argv, err := op.Argv()
	if err != nil {
		return err
	}
	out, postErr := ghCapture(argv)
	var created struct {
		ID int64 `json:"id"`
	}
	if postErr == nil {
		if err := json.Unmarshal(out, &created); err != nil || created.ID == 0 {
			postErr = fmt.Errorf("could not parse the review id from the response")
		}
	}
	if postErr != nil {
		if !r.HasContent() {
			return fmt.Errorf("gh %s: %w", shellJoin(argv), postErr)
		}
		// The create may have landed with its response lost, or an earlier
		// run's pending review blocks this one; either way, adopt it.
		fmt.Fprintf(os.Stderr, "gh-ff: creating the review failed (%v); looking for it on the server\n", postErr)
		id, err := recoverPending(r)
		if err != nil {
			return fmt.Errorf("gh %s: %w; recovery: %w", shellJoin(argv), postErr, err)
		}
		created.ID = id
		fmt.Printf("found pending review %d\n", id)
	}

	// Record the review id before fetching comment ids: if the fetch fails,
	// a rerun of submit must update this review instead of creating another.
	if err := spec.WriteIDs(path, created.ID, nil); err != nil {
		return fmt.Errorf("review %d created, but writing its id to %s failed: %w; recover with: gh-ff sync %s %d", created.ID, path, err, path, created.ID)
	}
	if !r.HasContent() {
		fmt.Printf("review %d submitted; id written to %s\n", created.ID, path)
		return nil
	}
	if err := syncIDs(r, path, created.ID); err != nil {
		return fmt.Errorf("review %d created pending, but %w; recover with: gh-ff sync %s %d", created.ID, err, path, created.ID)
	}
	if r.Event == "" {
		fmt.Printf("review %d created pending; ids written to %s\n", created.ID, path)
		return nil
	}
	if err := publish(r, created.ID); err != nil {
		return fmt.Errorf("review %d created pending with ids in %s, but publishing failed: %w; rerun submit to publish", created.ID, path, err)
	}
	fmt.Printf("review %d submitted; ids written to %s\n", created.ID, path)
	return nil
}

// recoverPending finds the authenticated user's pending review on the PR
// and returns its id if its content matches the file. GitHub shows a user
// only their own pending review and allows one per PR, so there is at most
// one candidate.
func recoverPending(r *spec.Review) (int64, error) {
	login, err := currentLogin()
	if err != nil {
		return 0, err
	}
	listOp := r.ListReviewsOp()
	listOp.Flags["paginate"] = nil
	listArgv, err := listOp.Argv()
	if err != nil {
		return 0, err
	}
	var pending *spec.APIReview
	// A review created a moment ago can be missing from the listing
	// (read-after-write lag), so look a few times before concluding the
	// create never landed.
	for attempt := 0; pending == nil; attempt++ {
		if attempt > 0 {
			if attempt == 3 {
				return 0, fmt.Errorf("no pending review by %s on pr %d", login, r.PR)
			}
			time.Sleep(time.Duration(1<<(attempt-1)) * time.Second)
		}
		out, err := ghCapture(listArgv)
		if err != nil {
			return 0, fmt.Errorf("listing reviews (gh %s): %w", shellJoin(listArgv), err)
		}
		reviews, err := spec.DecodePages[spec.APIReview](out)
		if err != nil {
			return 0, fmt.Errorf("review listing did not parse: %w", err)
		}
		for i, rv := range reviews {
			if rv.State == "PENDING" && rv.User.Login == login {
				pending = &reviews[i]
			}
		}
	}
	submitted, err := fetchReviewComments(r, pending.ID)
	if err != nil {
		return 0, err
	}
	if err := spec.MatchPending(r, pending.Body, submitted); err != nil {
		return 0, fmt.Errorf("your pending review %d on pr %d does not match this file (%w); submit or delete it on GitHub, or adopt it with gh-ff sync <file> %d", pending.ID, r.PR, err, pending.ID)
	}
	return pending.ID, nil
}

// publishIfPending publishes review id with the file's event if it is
// still pending; a published review's verdict cannot change.
func publishIfPending(r *spec.Review, id int64) error {
	if r.Event == "" {
		return nil
	}
	op := r.GetReviewOp(id)
	argv, err := op.Argv()
	if err != nil {
		return err
	}
	out, err := ghCapture(append(argv, "--jq", ".state"))
	if err != nil {
		return fmt.Errorf("checking review %d state (gh %s): %w", id, shellJoin(argv), err)
	}
	if strings.TrimSpace(string(out)) != "PENDING" {
		return nil
	}
	if err := publish(r, id); err != nil {
		return err
	}
	fmt.Printf("review %d published\n", id)
	return nil
}

func publish(r *spec.Review, id int64) error {
	op, err := r.PublishOp(id)
	if err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp("", "gh-ff-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	if err := op.Materialize(tmpDir, "publish-"); err != nil {
		return err
	}
	argv, err := op.Argv()
	if err != nil {
		return err
	}
	if _, err := ghCapture(argv); err != nil {
		return fmt.Errorf("gh %s: %w", shellJoin(argv), err)
	}
	return nil
}

// syncIDs fetches the review's comment ids, matches them to the file's
// comments, and writes all ids into the spec file.
func syncIDs(r *spec.Review, path string, reviewID int64) error {
	var ids []int64
	if len(r.Comments) > 0 {
		submitted, err := fetchReviewComments(r, reviewID)
		if err != nil {
			return err
		}
		if ids, err = spec.MatchCommentIDs(r.Comments, submitted); err != nil {
			return err
		}
	}
	if err := spec.WriteIDs(path, reviewID, ids); err != nil {
		return fmt.Errorf("writing ids back failed: %w", err)
	}
	return nil
}

func fetchReviewComments(r *spec.Review, reviewID int64) ([]spec.SubmittedComment, error) {
	listOp := r.ListCommentsOp(reviewID)
	listOp.Flags["paginate"] = nil
	listArgv, err := listOp.Argv()
	if err != nil {
		return nil, err
	}
	// The listing can 404 briefly right after creation (read-after-write
	// lag), so retry with backoff before giving up.
	var listOut []byte
	for attempt := 0; ; attempt++ {
		listOut, err = ghCapture(listArgv)
		if err == nil {
			break
		}
		if attempt == 3 {
			return nil, fmt.Errorf("fetching comment ids (gh %s) failed after %d attempts: %w", shellJoin(listArgv), attempt+1, err)
		}
		time.Sleep(time.Duration(1<<attempt) * time.Second)
	}
	submitted, err := spec.DecodePages[spec.SubmittedComment](listOut)
	if err != nil {
		return nil, fmt.Errorf("comment listing did not parse: %w", err)
	}
	return submitted, nil
}

func currentLogin() (string, error) {
	out, err := ghCapture([]string{"api", "user", "--jq", ".login"})
	if err != nil {
		return "", fmt.Errorf("resolving current user: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// syncReference fetches the PR's reviews, inline threads, and conversation
// and writes them to the file's reference section.
func syncReference(r *spec.Review, path string) error {
	fetch := func(op opsfile.Op) ([]byte, error) {
		op.Flags["paginate"] = nil
		argv, err := op.Argv()
		if err != nil {
			return nil, err
		}
		out, err := ghCapture(argv)
		if err != nil {
			return nil, fmt.Errorf("gh %s: %w", shellJoin(argv), err)
		}
		return out, nil
	}
	out, err := fetch(r.ListReviewsOp())
	if err != nil {
		return err
	}
	reviews, err := spec.DecodePages[spec.APIReview](out)
	if err != nil {
		return fmt.Errorf("review listing did not parse: %w", err)
	}
	if out, err = fetch(r.ListPRCommentsOp()); err != nil {
		return err
	}
	comments, err := spec.DecodePages[spec.APIReviewComment](out)
	if err != nil {
		return fmt.Errorf("review comment listing did not parse: %w", err)
	}
	if out, err = fetch(r.ListIssueCommentsOp()); err != nil {
		return err
	}
	issue, err := spec.DecodePages[spec.APIIssueComment](out)
	if err != nil {
		return fmt.Errorf("issue comment listing did not parse: %w", err)
	}
	if err := spec.WriteReference(path, spec.BuildReference(reviews, comments, issue)); err != nil {
		return fmt.Errorf("writing reference failed: %w", err)
	}
	return nil
}

// discoverReview finds the submitted review matching the spec: authored by
// the authenticated user with a body equal to the spec's summary.
func discoverReview(r *spec.Review) (int64, error) {
	login, err := currentLogin()
	if err != nil {
		return 0, err
	}
	listOp := r.ListReviewsOp()
	listArgv, err := listOp.Argv()
	if err != nil {
		return 0, err
	}
	out, err := ghCapture(listArgv)
	if err != nil {
		return 0, fmt.Errorf("listing reviews (gh %s): %w", shellJoin(listArgv), err)
	}
	var reviews []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := json.Unmarshal(out, &reviews); err != nil {
		return 0, fmt.Errorf("review listing did not parse: %w", err)
	}
	summary := strings.TrimSpace(r.Summary)
	var matches []int64
	for _, rv := range reviews {
		if rv.User.Login == login && strings.TrimSpace(rv.Body) == summary {
			matches = append(matches, rv.ID)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return 0, fmt.Errorf("no review by %s on pr %d matches the summary; pass the id: gh-ff sync <file> <review-id>", login, r.PR)
	default:
		return 0, fmt.Errorf("reviews %v by %s all match the summary; pass one: gh-ff sync <file> <review-id>", matches, login)
	}
}

// ghCapture runs gh with stdout captured (stderr and stdin pass through).
// On failure the captured stdout goes to stderr: gh api writes the HTTP
// error body (message, errors[]) to stdout and only a status line to stderr.
func ghCapture(argv []string) ([]byte, error) {
	cmd := exec.Command("gh", argv...)
	var buf bytes.Buffer
	cmd.Stdin = os.Stdin
	cmd.Stdout = &buf
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		os.Stderr.Write(buf.Bytes())
	}
	return buf.Bytes(), err
}

func execute(f *opsfile.File, dryRun bool) error {
	tmpDir, err := os.MkdirTemp("", "gh-ff-")
	if err != nil {
		return err
	}
	// Keep materialized files after a dry run so the printed commands stay
	// executable; clean up after a real run.
	if !dryRun {
		defer os.RemoveAll(tmpDir)
	}

	// Compile every op before executing any, so a malformed op later in
	// the file cannot leave earlier ops half-applied.
	argvs := make([][]string, len(f.Ops))
	for i, op := range f.Ops {
		if err := op.Materialize(tmpDir, fmt.Sprintf("op%d-", i)); err != nil {
			return fmt.Errorf("ops[%d]: %w", i, err)
		}
		argvs[i], err = op.Argv()
		if err != nil {
			return fmt.Errorf("ops[%d]: %w", i, err)
		}
	}

	for i, argv := range argvs {
		if dryRun {
			fmt.Println("gh " + shellJoin(argv))
			continue
		}
		cmd := exec.Command("gh", argv...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		// gh pages TTY stdout through $GH_PAGER/$PAGER (often less); force
		// pass-through so a multi-op run never blocks on a pager.
		cmd.Env = append(os.Environ(), "GH_PAGER=cat")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("ops[%d] (gh %s): %w", i, shellJoin(argv), err)
		}
	}
	return nil
}

// shellJoin quotes argv for display; single-quotes anything beyond [-=/@:.,{}%\w].
func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		if strings.ContainsFunc(a, func(r rune) bool {
			return !strings.ContainsRune("-=/@:.,{}%_", r) &&
				!(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9')
		}) || a == "" {
			quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		} else {
			quoted[i] = a
		}
	}
	return strings.Join(quoted, " ")
}

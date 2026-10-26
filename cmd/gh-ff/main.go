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

const usage = `usage:
  gh-ff run [-n] <ops.yaml>       execute a layer-1 ops file
  gh-ff compile <spec.yaml>       lower a layer-2 spec to ops YAML (stdout)
  gh-ff submit [-n] <spec.yaml>   compile a layer-2 spec and execute it
  gh-ff template <review|ops>     print a starter file for a format
  gh-ff sync <spec.yaml> [review-id]
      fetch server ids for an already-submitted review and write them into
      the file; discovers the review by author and summary when no id is
      in the file or given

  -n, --dry-run   print the gh commands without executing them
      --read-only abort unless every command is read-only
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gh-ff:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("expected a subcommand")
	}
	sub, rest := args[0], args[1:]
	switch sub {
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
		// Update mode (id present) and dry runs need no write-back.
		if *dryRun || d.Review.ID != 0 {
			return execute(f, *dryRun)
		}
		return submitNew(d, f, fs.Arg(0))
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

// submitNew executes a create-mode review submission, then writes the
// server-assigned review and comment ids back into the spec file so a
// later submit updates instead of duplicating.
func submitNew(d *spec.Doc, f *opsfile.File, path string) error {
	tmpDir, err := os.MkdirTemp("", "gh-ff-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	op := f.Ops[0] // create mode compiles to exactly one POST
	if err := op.Materialize(tmpDir, "op0-"); err != nil {
		return err
	}
	argv, err := op.Argv()
	if err != nil {
		return err
	}
	out, err := ghCapture(argv)
	if err != nil {
		return fmt.Errorf("gh %s: %w", shellJoin(argv), err)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(out, &created); err != nil || created.ID == 0 {
		return fmt.Errorf("review submitted, but could not parse its id from the response; ids not written to %s", path)
	}

	// Record the review id before fetching comment ids: if the fetch fails,
	// a rerun of submit must update this review instead of creating another.
	if err := spec.WriteIDs(path, created.ID, nil); err != nil {
		return fmt.Errorf("review %d submitted, but writing its id to %s failed: %w", created.ID, path, err)
	}
	if err := syncIDs(d.Review, path, created.ID); err != nil {
		return fmt.Errorf("review %d submitted, but %w; recover with: gh-ff sync %s %d", created.ID, err, path, created.ID)
	}
	fmt.Printf("review %d submitted; ids written to %s\n", created.ID, path)
	return nil
}

// syncIDs fetches the review's comment ids, matches them to the file's
// comments, and writes all ids into the spec file.
func syncIDs(r *spec.Review, path string, reviewID int64) error {
	var ids []int64
	if len(r.Comments) > 0 {
		listOp := r.ListCommentsOp(reviewID)
		listArgv, err := listOp.Argv()
		if err != nil {
			return err
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
				return fmt.Errorf("fetching comment ids (gh %s) failed after %d attempts: %w", shellJoin(listArgv), attempt+1, err)
			}
			time.Sleep(time.Duration(1<<attempt) * time.Second)
		}
		var submitted []spec.SubmittedComment
		if err := json.Unmarshal(listOut, &submitted); err != nil {
			return fmt.Errorf("comment listing did not parse: %w", err)
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

// discoverReview finds the submitted review matching the spec: authored by
// the authenticated user with a body equal to the spec's summary.
func discoverReview(r *spec.Review) (int64, error) {
	loginOut, err := ghCapture([]string{"api", "user", "--jq", ".login"})
	if err != nil {
		return 0, fmt.Errorf("resolving current user: %w", err)
	}
	login := strings.TrimSpace(string(loginOut))
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

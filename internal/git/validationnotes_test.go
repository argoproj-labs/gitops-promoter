package git_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/validations"
)

const validationsNotesName = "promoter.validations"

// fetchNotes brings the work tree's copy of the validation notes up to date with the remote.
func (d *dryRepo) fetchNotes() {
	GinkgoHelper()
	if d.remoteRef(git.PromoterValidationsNotesRef) == "" {
		return
	}
	mustGit(d.workDir, "fetch", "origin", "+"+git.PromoterValidationsNotesRef+":"+git.PromoterValidationsNotesRef)
}

// validateByHand appends a validation line to the note on sha and pushes it, as the docs tell people to.
func (d *dryRepo) validateByHand(sha, line string) {
	GinkgoHelper()
	d.fetchNotes()
	mustGit(d.workDir, "notes", "--ref="+validationsNotesName, "append", "-m", line, sha)
	mustGit(d.workDir, "push", "origin", git.PromoterValidationsNotesRef)
}

// summarize renders events as "<target> <source> <sha>" for compact assertions.
func summarize(events []git.ValidationEvent) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, event.Line.Target+" "+event.Line.Source+" "+event.Sha)
	}
	return out
}

var _ = Describe("Validation notes", func() {
	const (
		tagStaging = "environment/staging-tag"
		tagProd    = "environment/prod-tag"
	)

	var (
		ctx    context.Context
		dry    *dryRepo
		first  string
		second string
	)

	automatic := func(target string) validations.Line {
		return validations.Line{Target: target, Source: validations.SourceAutomatic, By: "environment/dev successful", At: time.Now()}
	}

	BeforeEach(func() {
		ctx = context.Background()
		dry = newDryRepo()
		first = dry.head()
		second = dry.commit("second")
		dry.push()
	})

	It("lists nothing while the notes ref does not exist", func() {
		ops := dry.newOps("default/gate")
		refs, err := ops.SyncDryRefs(ctx, dry.branch, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(refs.NotesTip).To(BeEmpty())

		events, ok, err := ops.ListValidations(ctx, "", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(events).To(BeEmpty())

		By("Treating a cursor as lost when there is no notes ref for it to be part of")
		_, ok, err = ops.ListValidations(ctx, first, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	It("records validations and lists them in the order they were written", func() {
		ops := dry.newOps("default/gate")
		tip, err := ops.AppendValidations(ctx, []git.ValidationEntry{
			{Sha: first, Line: automatic(tagStaging)},
			{Sha: second, Line: automatic(tagStaging)},
			{Sha: first, Line: automatic(tagProd)},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(tip).To(Equal(dry.remoteRef(git.PromoterValidationsNotesRef)))

		events, ok, err := ops.ListValidations(ctx, "", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(summarize(events)).To(Equal([]string{
			tagStaging + " Automatic " + first,
			tagStaging + " Automatic " + second,
			tagProd + " Automatic " + first,
		}))
		Expect(events[2].NotesCommit).To(Equal(tip))
		Expect(events[0].By()).To(Equal("environment/dev successful"))
		Expect(events[0].At()).To(BeTemporally("~", time.Now(), time.Minute))

		By("Keeping both validations of the same commit as separate lines of one note")
		note := mustGit(dry.remoteDir, "notes", "--ref="+validationsNotesName, "show", first)
		Expect(validations.ParseNote(note)).To(HaveLen(2))
	})

	It("lists only what was written after a cursor", func() {
		ops := dry.newOps("default/gate")
		cursor, err := ops.AppendValidations(ctx, []git.ValidationEntry{{Sha: first, Line: automatic(tagStaging)}})
		Expect(err).NotTo(HaveOccurred())

		events, ok, err := ops.ListValidations(ctx, cursor, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(events).To(BeEmpty())

		_, err = ops.AppendValidations(ctx, []git.ValidationEntry{{Sha: second, Line: automatic(tagStaging)}})
		Expect(err).NotTo(HaveOccurred())

		events, ok, err = ops.ListValidations(ctx, cursor, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(summarize(events)).To(Equal([]string{tagStaging + " Automatic " + second}))
	})

	It("reads a validation a person pushed by hand", func() {
		dry.validateByHand(second, `{"target":"`+tagProd+`","reason":"hotfix"}`)

		ops := dry.newOps("default/gate")
		refs, err := ops.SyncDryRefs(ctx, dry.branch, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(refs.NotesTip).To(Equal(dry.remoteRef(git.PromoterValidationsNotesRef)))

		events, ok, err := ops.ListValidations(ctx, "", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(events).To(HaveLen(1))
		Expect(events[0].Sha).To(Equal(second))
		Expect(events[0].Line.Source).To(Equal(validations.SourceGit))
		Expect(events[0].Line.Reason).To(Equal("hotfix"))
		Expect(events[0].Line.Automatic()).To(BeFalse())

		By("Taking who and when from the notes commit, since the line does not say")
		Expect(events[0].By()).To(Equal("jane@example.com"))
		Expect(events[0].At()).To(BeTemporally("~", time.Now(), time.Minute))
		Expect(events[0].NotesCommit).To(Equal(refs.NotesTip))
	})

	It("keeps a person's validation and the controller's on the same commit", func() {
		ops := dry.newOps("default/gate")
		_, err := ops.AppendValidations(ctx, []git.ValidationEntry{{Sha: first, Line: automatic(tagStaging)}})
		Expect(err).NotTo(HaveOccurred())
		dry.validateByHand(first, `{"target":"`+tagProd+`"}`)

		ops = dry.newOps("default/gate")
		_, err = ops.AppendValidations(ctx, []git.ValidationEntry{{Sha: first, Line: automatic(tagProd)}})
		Expect(err).NotTo(HaveOccurred())

		events, _, err := ops.ListValidations(ctx, "", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(summarize(events)).To(Equal([]string{
			tagStaging + " Automatic " + first,
			tagProd + " Git " + first,
			tagProd + " Automatic " + first,
		}))
	})

	It("ignores note lines that are not validations", func() {
		dry.validateByHand(first, "reviewed by jane")
		dry.validateByHand(first, `{"reason":"no target"}`)
		dry.validateByHand(first, `{"target":"`+tagProd+`"}`)

		ops := dry.newOps("default/gate")
		_, err := ops.SyncDryRefs(ctx, dry.branch, nil)
		Expect(err).NotTo(HaveOccurred())
		events, _, err := ops.ListValidations(ctx, "", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(summarize(events)).To(Equal([]string{tagProd + " Git " + first}))
	})

	It("does not record a request twice when a write is retried", func() {
		ops := dry.newOps("default/validation")
		line := validations.Line{Target: tagProd, Source: validations.SourceResource, By: "CommitValidation/demo-prod", ID: "uid-1/7"}
		tip, err := ops.AppendValidations(ctx, []git.ValidationEntry{{Sha: second, Line: line}})
		Expect(err).NotTo(HaveOccurred())

		By("Appending the same request again from a fresh clone, as after a crash before status was written")
		ops = dry.newOps("default/validation")
		again, err := ops.AppendValidations(ctx, []git.ValidationEntry{{Sha: second, Line: line}})
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(tip))
		Expect(dry.remoteRef(git.PromoterValidationsNotesRef)).To(Equal(tip))

		By("Recording a different request for the same commit")
		line.ID = "uid-1/9"
		_, err = ops.AppendValidations(ctx, []git.ValidationEntry{{Sha: second, Line: line}})
		Expect(err).NotTo(HaveOccurred())
		events, _, err := ops.ListValidations(ctx, "", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(2))
	})

	It("validates a commit that is not in the clone", func() {
		mustGit(dry.workDir, "checkout", "-b", "hotfix", first)
		hotfix := dry.commit("hotfix")
		mustGit(dry.workDir, "push", "origin", "hotfix")

		ops := dry.newOps("default/validation")
		Expect(ops.HasCommit(ctx, hotfix)).To(BeFalse())
		_, err := ops.AppendValidations(ctx, []git.ValidationEntry{{Sha: hotfix, Line: validations.Line{Target: tagProd, Source: validations.SourceResource}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(ops.HasCommit(ctx, hotfix)).To(BeFalse(), "recording a validation should not download the commit")

		events, _, err := ops.ListValidations(ctx, "", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(summarize(events)).To(Equal([]string{tagProd + " Resource " + hotfix}))
	})

	It("refuses to validate something that is not a full SHA", func() {
		ops := dry.newOps("default/validation")
		_, err := ops.AppendValidations(ctx, []git.ValidationEntry{{Sha: "main", Line: automatic(tagProd)}})
		Expect(err).To(MatchError(ContainSubstring("not a full git object ID")))
		Expect(dry.remoteRef(git.PromoterValidationsNotesRef)).To(BeEmpty())
	})

	It("keeps every validation when several writers push at once", func() {
		const writers = 6
		shas := make([]string, writers)
		for i := range writers {
			shas[i] = dry.commit(fmt.Sprintf("commit %d", i))
		}
		dry.push()
		clones := make([]*git.EnvironmentOperations, writers)
		for i := range writers {
			clones[i] = dry.newOps(fmt.Sprintf("default/writer-%d", i))
		}

		errs := runConcurrently(writers, func(i int) error {
			if _, err := clones[i].AppendValidations(ctx, []git.ValidationEntry{{Sha: shas[i], Line: automatic(tagStaging)}}); err != nil {
				return fmt.Errorf("writer %d: %w", i, err)
			}
			return nil
		})
		for _, err := range errs {
			Expect(err).NotTo(HaveOccurred())
		}

		reader := dry.newOps("default/reader")
		_, err := reader.SyncDryRefs(ctx, dry.branch, nil)
		Expect(err).NotTo(HaveOccurred())
		events, ok, err := reader.ListValidations(ctx, "", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		validated := make([]string, 0, len(events))
		for _, event := range events {
			validated = append(validated, event.Sha)
		}
		Expect(validated).To(ConsistOf(shas))
	})

	It("lists each side of a notes merge once, and nothing from before the cursor", func() {
		ops := dry.newOps("default/gate")
		_, err := ops.AppendValidations(ctx, []git.ValidationEntry{{Sha: first, Line: automatic(tagStaging)}})
		Expect(err).NotTo(HaveOccurred())

		By("A person starting from the notes as they are now")
		dry.fetchNotes()

		cursor, err := ops.AppendValidations(ctx, []git.ValidationEntry{{Sha: first, Line: automatic(tagProd)}})
		Expect(err).NotTo(HaveOccurred())
		_, err = ops.AppendValidations(ctx, []git.ValidationEntry{{Sha: second, Line: automatic(tagStaging)}})
		Expect(err).NotTo(HaveOccurred())

		By("That person validating on their stale copy, then merging the remote notes into it and pushing the merge")
		mustGit(dry.workDir, "notes", "--ref="+validationsNotesName, "append", "-m", `{"target":"`+tagProd+`","reason":"by hand"}`, second)
		mustGit(dry.workDir, "fetch", "origin", "+"+git.PromoterValidationsNotesRef+":refs/notes/remote-validations")
		mustGit(dry.workDir, "notes", "--ref="+validationsNotesName, "merge", "--strategy=cat_sort_uniq", "refs/notes/remote-validations")
		mustGit(dry.workDir, "push", "origin", git.PromoterValidationsNotesRef)

		reader := dry.newOps("default/reader")
		_, err = reader.SyncDryRefs(ctx, dry.branch, nil)
		Expect(err).NotTo(HaveOccurred())
		events, ok, err := reader.ListValidations(ctx, cursor, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue(), "the cursor is still part of the merged history")
		Expect(summarize(events)).To(ConsistOf(
			tagStaging+" Automatic "+second,
			tagProd+" Git "+second,
		))
	})

	It("does not read a re-arranged notes tree as new validations", func() {
		ops := dry.newOps("default/gate")
		cursor, err := ops.AppendValidations(ctx, []git.ValidationEntry{{Sha: first, Line: automatic(tagStaging)}})
		Expect(err).NotTo(HaveOccurred())

		By("Moving the note from <sha> to <sha[:2]>/<sha[2:]>, as git does when a notes tree grows")
		dry.fetchNotes()
		blob := strings.Fields(mustGit(dry.workDir, "ls-tree", git.PromoterValidationsNotesRef, first))[2]
		subtree := mktree(dry.workDir, "100644 blob "+blob+"\t"+first[2:]+"\n")
		root := mktree(dry.workDir, "040000 tree "+subtree+"\t"+first[:2]+"\n")
		commit := strings.TrimSpace(mustGit(dry.workDir, "commit-tree", root, "-p", cursor, "-m", "fan out"))
		mustGit(dry.workDir, "update-ref", git.PromoterValidationsNotesRef, commit)
		mustGit(dry.workDir, "push", "origin", git.PromoterValidationsNotesRef)
		Expect(mustGit(dry.workDir, "notes", "--ref="+validationsNotesName, "show", first)).To(ContainSubstring(tagStaging))

		reader := dry.newOps("default/reader")
		_, err = reader.SyncDryRefs(ctx, dry.branch, nil)
		Expect(err).NotTo(HaveOccurred())
		events, ok, err := reader.ListValidations(ctx, cursor, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(events).To(BeEmpty())

		By("Still listing the validation once from the beginning")
		events, _, err = reader.ListValidations(ctx, "", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(summarize(events)).To(Equal([]string{tagStaging + " Automatic " + first}))
	})

	It("reports a cursor that is no longer part of the notes history", func() {
		ops := dry.newOps("default/gate")
		cursor, err := ops.AppendValidations(ctx, []git.ValidationEntry{{Sha: first, Line: automatic(tagStaging)}})
		Expect(err).NotTo(HaveOccurred())

		By("Someone replacing the notes ref with unrelated history")
		mustGit(dry.workDir, "notes", "--ref=rewritten", "add", "-m", `{"target":"`+tagProd+`"}`, second)
		mustGit(dry.workDir, "push", "--force", "origin", "refs/notes/rewritten:"+git.PromoterValidationsNotesRef)

		reader := dry.newOps("default/reader")
		_, err = reader.SyncDryRefs(ctx, dry.branch, nil)
		Expect(err).NotTo(HaveOccurred())
		events, ok, err := reader.ListValidations(ctx, cursor, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		Expect(events).To(BeEmpty())

		By("Listing the new history from the beginning")
		events, ok, err = reader.ListValidations(ctx, "", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(summarize(events)).To(Equal([]string{tagProd + " Git " + second}))
	})

	It("keeps only the newest notes commits when given a limit", func() {
		ops := dry.newOps("default/gate")
		for _, target := range []string{"a-tag", "b-tag", "c-tag"} {
			_, err := ops.AppendValidations(ctx, []git.ValidationEntry{{Sha: first, Line: automatic(target)}})
			Expect(err).NotTo(HaveOccurred())
		}
		events, ok, err := ops.ListValidations(ctx, "", 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(summarize(events)).To(Equal([]string{"b-tag Automatic " + first, "c-tag Automatic " + first}))
	})

	It("leaves nothing behind locally when the remote refuses the push", func() {
		ops := dry.newOps("default/gate")
		cursor, err := ops.AppendValidations(ctx, []git.ValidationEntry{{Sha: first, Line: automatic(tagStaging)}})
		Expect(err).NotTo(HaveOccurred())

		hook := filepath.Join(dry.remoteDir, "hooks", "pre-receive")
		Expect(os.WriteFile(hook, []byte("#!/bin/sh\necho 'notes are read-only' >&2\nexit 1\n"), 0o755)).To(Succeed())

		_, err = ops.AppendValidations(ctx, []git.ValidationEntry{{Sha: second, Line: automatic(tagStaging)}})
		Expect(err).To(MatchError(ContainSubstring("failed to push validation notes")))
		Expect(dry.remoteRef(git.PromoterValidationsNotesRef)).To(Equal(cursor))

		By("Not reading the validation that never reached the remote")
		events, ok, err := ops.ListValidations(ctx, cursor, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(events).To(BeEmpty())
	})

	It("has the note contents locally after a sync, so reading them needs no network", func() {
		writer := dry.newOps("default/writer")
		_, err := writer.AppendValidations(ctx, []git.ValidationEntry{{Sha: first, Line: automatic(tagStaging)}})
		Expect(err).NotTo(HaveOccurred())

		reader := dry.newOps("default/reader")
		_, err = reader.SyncDryRefs(ctx, dry.branch, nil)
		Expect(err).NotTo(HaveOccurred())
		blob := strings.Fields(mustGit(dry.remoteDir, "ls-tree", git.PromoterValidationsNotesRef, first))[2]
		Expect(gitObjectPresent(reader.ClonePath(), blob)).To(BeTrue())
	})

	It("drops the local notes ref when the remote one is deleted", func() {
		ops := dry.newOps("default/gate")
		_, err := ops.AppendValidations(ctx, []git.ValidationEntry{{Sha: first, Line: automatic(tagStaging)}})
		Expect(err).NotTo(HaveOccurred())
		mustGit(dry.workDir, "push", "origin", ":"+git.PromoterValidationsNotesRef)

		ops = dry.newOps("default/gate")
		refs, err := ops.SyncDryRefs(ctx, dry.branch, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(refs.NotesTip).To(BeEmpty())
		events, ok, err := ops.ListValidations(ctx, "", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(events).To(BeEmpty())
	})

	Describe("AddedLines", func() {
		It("returns the lines the update added, in order", func() {
			// Blank lines carry nothing, so where one lands in the result does not matter.
			added := slices.DeleteFunc(git.AddedLines("a\nb\n", "a\nb\n\nc\nd\n"), func(line string) bool { return line == "" })
			Expect(added).To(Equal([]string{"c", "d"}))
		})

		It("counts a repeated line as new only as often as it was added", func() {
			Expect(git.AddedLines("a\n", "a\na\na\n")).To(Equal([]string{"a", "a"}))
		})

		It("returns nothing when lines were only removed or reordered", func() {
			Expect(git.AddedLines("a\nb\nc\n", "c\na\n")).To(BeEmpty())
		})

		It("returns every line of a new note", func() {
			Expect(git.AddedLines("", "a\nb")).To(Equal([]string{"a", "b"}))
		})
	})
})

// mktree writes a tree object from ls-tree formatted input and returns its ID.
func mktree(dir, input string) string {
	GinkgoHelper()
	cmd := exec.CommandContext(context.Background(), "git", "mktree")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(out))
	return strings.TrimSpace(string(out))
}

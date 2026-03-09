package git

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

func TestGitInternal(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)
	RunSpecs(t, "Git Internal Suite")
}

var _ = Describe("sanitizeReferences", func() {
	ctx := context.Background()

	DescribeTable("Sha validation",
		func(sha string, expectCleared bool) {
			refs := []v1alpha1.RevisionReference{
				{
					Commit: &v1alpha1.CommitMetadata{
						Sha:     sha,
						RepoURL: "https://github.com/example/repo",
					},
				},
			}
			result := sanitizeReferences(ctx, refs)
			Expect(result).To(HaveLen(1))
			Expect(result[0].Commit).NotTo(BeNil())
			if expectCleared {
				Expect(result[0].Commit.Sha).To(BeEmpty(), "expected Sha to be cleared for value %q", sha)
			} else {
				Expect(result[0].Commit.Sha).To(Equal(sha), "expected Sha to be preserved for value %q", sha)
			}
		},
		Entry("valid SHA-1 (40 hex chars) is preserved", "c4c862564afe56abf8cc8ac683eee3dc8bf96108", false),
		Entry("valid SHA-256 (64 hex chars) is preserved", "c4c862564afe56abf8cc8ac683eee3dc8bf96108c4c862564afe56abf8cc8ac6", false),
		Entry("invalid: too short is cleared", "abc123", true),
		Entry("invalid: uppercase hex is cleared", "C4C862564AFE56ABF8CC8AC683EEE3DC8BF96108", true),
		Entry("invalid: non-hex chars is cleared", "not-a-valid-sha-at-all-xxxxxxxxxxxxxxxxxxxx", true),
		Entry("invalid: 39 hex chars is cleared", "c4c862564afe56abf8cc8ac683eee3dc8bf9610", true),
		Entry("invalid: 41 hex chars is cleared", "c4c862564afe56abf8cc8ac683eee3dc8bf961080", true),
		Entry("empty Sha is preserved as-is (no change)", "", false),
	)

	DescribeTable("RepoURL validation",
		func(repoURL string, expectCleared bool) {
			refs := []v1alpha1.RevisionReference{
				{
					Commit: &v1alpha1.CommitMetadata{
						Sha:     "c4c862564afe56abf8cc8ac683eee3dc8bf96108",
						RepoURL: repoURL,
					},
				},
			}
			result := sanitizeReferences(ctx, refs)
			Expect(result).To(HaveLen(1))
			Expect(result[0].Commit).NotTo(BeNil())
			if expectCleared {
				Expect(result[0].Commit.RepoURL).To(BeEmpty(), "expected RepoURL to be cleared for value %q", repoURL)
			} else {
				Expect(result[0].Commit.RepoURL).To(Equal(repoURL), "expected RepoURL to be preserved for value %q", repoURL)
			}
		},
		Entry("valid https URL is preserved", "https://github.com/example/repo", false),
		Entry("valid http URL is preserved", "http://github.example.com/example/repo", false),
		Entry("empty RepoURL is preserved as-is (no change)", "", false),
		Entry("ftp URL is cleared", "ftp://invalid.example.com/repo", true),
		Entry("ssh URL is cleared", "ssh://git@github.com/example/repo", true),
		Entry("git protocol URL is cleared", "git://github.com/example/repo", true),
		Entry("relative path is cleared", "example/repo", true),
		Entry("bare hostname without scheme is cleared", "github.com/example/repo", true),
		Entry("https URL with no path is preserved", "https://", false),
	)

	Context("when refs slice is nil", func() {
		It("returns nil without panicking", func() {
			result := sanitizeReferences(ctx, nil)
			Expect(result).To(BeNil())
		})
	})

	Context("when refs slice is empty", func() {
		It("returns an empty slice", func() {
			result := sanitizeReferences(ctx, []v1alpha1.RevisionReference{})
			Expect(result).To(BeEmpty())
		})
	})

	Context("when a reference has a nil Commit", func() {
		It("skips it without panicking and returns it unchanged", func() {
			refs := []v1alpha1.RevisionReference{
				{Commit: nil},
			}
			result := sanitizeReferences(ctx, refs)
			Expect(result).To(HaveLen(1))
			Expect(result[0].Commit).To(BeNil())
		})
	})

	Context("when a reference has both invalid Sha and invalid RepoURL", func() {
		It("clears both fields but keeps the reference item with other fields intact", func() {
			refs := []v1alpha1.RevisionReference{
				{
					Commit: &v1alpha1.CommitMetadata{
						Sha:     "not-a-valid-sha",
						RepoURL: "ftp://bad.example.com",
						Subject: "Keep me",
						Author:  "Someone <someone@example.com>",
						Body:    "Keep body too",
						Date:    func() *metav1.Time { t := metav1.Now(); return &t }(),
					},
				},
			}
			result := sanitizeReferences(ctx, refs)
			Expect(result).To(HaveLen(1))
			c := result[0].Commit
			Expect(c).NotTo(BeNil())
			Expect(c.Sha).To(BeEmpty())
			Expect(c.RepoURL).To(BeEmpty())
			Expect(c.Subject).To(Equal("Keep me"))
			Expect(c.Author).To(Equal("Someone <someone@example.com>"))
			Expect(c.Body).To(Equal("Keep body too"))
			Expect(c.Date).NotTo(BeNil())
		})
	})

	Context("when multiple references are present with mixed validity", func() {
		It("sanitizes only invalid entries and preserves valid ones", func() {
			refs := []v1alpha1.RevisionReference{
				{
					Commit: &v1alpha1.CommitMetadata{
						Sha:     "c4c862564afe56abf8cc8ac683eee3dc8bf96108",
						RepoURL: "https://github.com/valid/repo",
						Subject: "Valid entry",
					},
				},
				{
					Commit: &v1alpha1.CommitMetadata{
						Sha:     "bad-sha",
						RepoURL: "ftp://bad.example.com",
						Subject: "Invalid entry",
					},
				},
				{Commit: nil},
			}
			result := sanitizeReferences(ctx, refs)
			Expect(result).To(HaveLen(3))

			// First entry: valid - both fields preserved.
			Expect(result[0].Commit.Sha).To(Equal("c4c862564afe56abf8cc8ac683eee3dc8bf96108"))
			Expect(result[0].Commit.RepoURL).To(Equal("https://github.com/valid/repo"))
			Expect(result[0].Commit.Subject).To(Equal("Valid entry"))

			// Second entry: both fields cleared, subject kept.
			Expect(result[1].Commit.Sha).To(BeEmpty())
			Expect(result[1].Commit.RepoURL).To(BeEmpty())
			Expect(result[1].Commit.Subject).To(Equal("Invalid entry"))

			// Third entry: nil Commit unchanged.
			Expect(result[2].Commit).To(BeNil())
		})
	})

	Context("when Sha is longer than 64 characters", func() {
		It("clears the overlong Sha without panicking (truncation for logging only)", func() {
			overlongSha := "c4c862564afe56abf8cc8ac683eee3dc8bf96108c4c862564afe56abf8cc8ac6extra"
			refs := []v1alpha1.RevisionReference{
				{
					Commit: &v1alpha1.CommitMetadata{
						Sha:     overlongSha,
						RepoURL: "https://github.com/example/repo",
					},
				},
			}
			result := sanitizeReferences(ctx, refs)
			Expect(result[0].Commit.Sha).To(BeEmpty())
		})
	})

	Context("when RepoURL is longer than 100 characters", func() {
		It("clears the overlong invalid RepoURL without panicking (truncation for logging only)", func() {
			longInvalidURL := "ftp://" + string(make([]byte, 200))
			refs := []v1alpha1.RevisionReference{
				{
					Commit: &v1alpha1.CommitMetadata{
						Sha:     "c4c862564afe56abf8cc8ac683eee3dc8bf96108",
						RepoURL: longInvalidURL,
					},
				},
			}
			result := sanitizeReferences(ctx, refs)
			Expect(result[0].Commit.RepoURL).To(BeEmpty())
		})
	})
})

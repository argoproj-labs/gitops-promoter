/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package repourl_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/argoproj-labs/gitops-promoter/internal/utils/repourl"
)

var _ = Describe("ConvertToWebURL", func() {
	DescribeTable("normalizes git remote URLs for web commit links",
		func(raw, want string) {
			got, err := repourl.ConvertToWebURL(raw)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("https with .git suffix", "https://github.com/org/repo.git", "https://github.com/org/repo"),
		Entry("https without .git", "https://github.com/org/repo", "https://github.com/org/repo"),
		Entry("scp-style ssh", "git@github.com:org/repo.git", "https://github.com/org/repo"),
		Entry("ssh URL", "ssh://git@github.com/org/repo.git", "https://github.com/org/repo"),
		Entry("https with embedded git user", "https://git@github.com/org/repo", "https://github.com/org/repo"),
		Entry("https with embedded credentials", "https://user:secret@github.com/org/repo", "https://github.com/org/repo"),
		Entry("ssh URL with port", "ssh://git@github.com:2222/org/repo.git", "https://github.com:2222/org/repo"),
		Entry("trailing slash", "https://github.com/org/repo/", "https://github.com/org/repo"),
	)

	It("returns an error for malformed scp-style input", func() {
		_, err := repourl.ConvertToWebURL("git@host")
		Expect(err).To(HaveOccurred())
	})

	It("returns an error for empty input", func() {
		_, err := repourl.ConvertToWebURL("")
		Expect(err).To(HaveOccurred())
	})

	It("returns an error for unsupported schemes", func() {
		_, err := repourl.ConvertToWebURL("ftp://example.com/org/repo")
		Expect(err).To(HaveOccurred())
	})
})

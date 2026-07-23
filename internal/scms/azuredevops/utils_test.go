package azuredevops_test

import (
	"errors"
	"net/http"

	"github.com/argoproj-labs/gitops-promoter/internal/scms/azuredevops"
	ado "github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Azure DevOps HTTP errors", func() {
	DescribeTable("azureDevOpsHTTPStatusCode",
		func(err error, wantCode int, wantOK bool) {
			gotCode, gotOK := azuredevops.AzureDevOpsHTTPStatusCode(err)
			Expect(gotOK).To(Equal(wantOK))
			Expect(gotCode).To(Equal(wantCode))
		},
		Entry("nil error", nil, 0, false),
		Entry("pointer wrapped error", func() error {
			notFound := http.StatusNotFound
			return &ado.WrappedError{StatusCode: &notFound}
		}(), http.StatusNotFound, true),
		Entry("value wrapped error", func() error {
			serverError := http.StatusInternalServerError
			return ado.WrappedError{StatusCode: &serverError}
		}(), http.StatusInternalServerError, true),
		Entry("unrelated error", errors.New("network timeout"), 0, false),
	)

	DescribeTable("isAzureDevOpsNotFound",
		func(err error, want bool) {
			Expect(azuredevops.IsAzureDevOpsNotFound(err)).To(Equal(want))
		},
		Entry("nil error", nil, false),
		Entry("404 wrapped error", func() error {
			notFound := http.StatusNotFound
			return &ado.WrappedError{StatusCode: &notFound}
		}(), true),
		Entry("404 value wrapped error", func() error {
			notFound := http.StatusNotFound
			return ado.WrappedError{StatusCode: &notFound}
		}(), true),
		Entry("500 wrapped error", func() error {
			serverError := http.StatusInternalServerError
			return &ado.WrappedError{StatusCode: &serverError}
		}(), false),
		Entry("500 value wrapped error", func() error {
			serverError := http.StatusInternalServerError
			return ado.WrappedError{StatusCode: &serverError}
		}(), false),
		Entry("unrelated error", errors.New("network timeout"), false),
	)
})

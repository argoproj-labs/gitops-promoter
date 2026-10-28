// deadcode is a standalone check kept in its own module so x/tools does not
// become a direct dependency of the operator module.
module github.com/argoproj-labs/gitops-promoter/hack/deadcode

go 1.27.0

require golang.org/x/tools v0.52.0

require (
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
)

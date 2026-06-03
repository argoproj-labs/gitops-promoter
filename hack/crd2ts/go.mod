// crd2ts is a standalone dev tool kept in its own module so it does not
// pollute the root module dependency tree.
module github.com/argoproj-labs/gitops-promoter/hack/crd2ts

go 1.26.4

require sigs.k8s.io/yaml v1.6.0

require (
	go.yaml.in/yaml/v2 v2.4.3 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
)

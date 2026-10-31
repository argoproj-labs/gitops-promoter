package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/test/scmserver"
)

func must[T any](v T, err error) T {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return v
}

func main() {
	ctx := context.Background()
	admin := scmserver.NewAdminClient("http://localhost:9000")
	environments := []string{"environment/development", "environment/staging"}
	if len(os.Args) > 1 && os.Args[1] == "hydrate" {
		var id int64
		fmt.Sscan(os.Args[2], &id)
		repo := must(admin.Repo(ctx, id))
		fmt.Println("dry:", hydrate(ctx, admin, repo, environments))
		return
	}
	repo := must(admin.CreateRepo(ctx, scmserver.CreateRepoRequest{Provider: scmserver.GitLab, Owner: "acme", Name: "deploy"}))
	first := must(admin.Commit(ctx, repo.ID, scmserver.CommitRequest{
		Branch: repo.DefaultBranch, Files: map[string]string{"hydrator.metadata": `{"drySha": "n/a"}`}, Message: "init commit dry side n/a dry sha",
	}))
	must(admin.Commit(ctx, repo.ID, scmserver.CommitRequest{
		Branch: repo.DefaultBranch, Files: map[string]string{"hydrator.metadata": fmt.Sprintf(`{"drySha": %q}`, first.SHA)}, Message: "second commit with real dry sha",
	}))
	for _, env := range environments {
		next := must(admin.Commit(ctx, repo.ID, scmserver.CommitRequest{Branch: env + "-next", Orphan: true, Message: "initial empty commit for " + env + "-next"}))
		must(admin.SetBranch(ctx, repo.ID, env, scmserver.BranchRequest{SHA: next.SHA}))
	}
	dry := hydrate(ctx, admin, repo, environments)
	out, _ := json.Marshal(map[string]any{"id": repo.ID, "owner": repo.Owner, "name": repo.Name, "defaultBranch": repo.DefaultBranch, "cloneURL": repo.CloneURL, "dry": dry})
	fmt.Println(string(out))
}

func hydrate(ctx context.Context, admin *scmserver.AdminClient, repo scmserver.RepoInfo, environments []string) string {
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	dry := must(admin.Commit(ctx, repo.ID, scmserver.CommitRequest{
		Branch: repo.DefaultBranch, Files: map[string]string{"manifests-fake.yaml": fmt.Sprintf(`{"time": %q}`, stamp)},
		Message: "change the application", AuthorName: "developer", AuthorEmail: "developer@example.com",
	}))
	metadata := must(json.MarshalIndent(v1alpha1.HydratorMetadata{DrySha: dry.SHA, Author: "developer <developer@example.com>", Date: metav1.Now(), Subject: "change the application"}, "", "\t"))
	for _, env := range environments {
		must(admin.Commit(ctx, repo.ID, scmserver.CommitRequest{
			Branch: env + "-next",
			Files:  map[string]string{"hydrator.metadata": string(metadata), "manifests-fake.yaml": fmt.Sprintf(`{"time": %q, "environment": %q}`, stamp, env)},
			Message: "hydrate " + dry.SHA + " to " + env, AuthorName: "hydrator", AuthorEmail: "hydrator@example.com",
		}))
	}
	return dry.SHA
}

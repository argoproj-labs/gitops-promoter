# Example DRY Terraform repository

This is the layout the GitOps Promoter Terraform hydrator expects:

```
modules/<name>/          shared modules, copied into every hydrated commit
environments/<env>/      one root module per environment (main.tf, backend.tf, terraform.tfvars)
```

The hydrator renders `environments/<env>` plus `modules/` onto the `environment/<env>-next`
branch and adds `hydrator.metadata` and `plan.txt` at the root. The applier runs in
`environments/<env>` on the `environment/<env>` branch.

The example uses the built-in `terraform_data` resource and a local backend so it works
offline. Replace `backend.tf` with your remote backend (S3, GCS, Azure, HCP Terraform, ...)
before using it for real infrastructure.

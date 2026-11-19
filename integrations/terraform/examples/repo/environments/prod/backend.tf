# Replace with your remote backend before managing real infrastructure, e.g.
#
#   backend "s3" {
#     bucket = "my-tf-state"
#     key    = "greeting/prod.tfstate"
#     region = "us-east-1"
#   }
#
# The hydrate workflow needs read access to state to produce plan.txt; the
# apply workflow needs read/write access.
terraform {
  backend "local" {
    path = "terraform.tfstate"
  }
}

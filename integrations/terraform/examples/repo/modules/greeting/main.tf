terraform {
  required_version = ">= 1.4.0"
}

# terraform_data is built into Terraform and OpenTofu, so this module needs no
# provider download. Replace it with real resources.
resource "terraform_data" "greeting" {
  input = "Hello from ${var.environment}, ${var.audience}!"
}

terraform {
  required_version = ">= 1.4.0"
}

module "greeting" {
  source = "../../modules/greeting"

  environment = var.environment
  audience    = var.audience
}

variable "environment" {
  type = string
}

variable "audience" {
  type = string
}

output "message" {
  value = module.greeting.message
}

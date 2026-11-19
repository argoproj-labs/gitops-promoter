output "message" {
  description = "The rendered greeting."
  value       = terraform_data.greeting.output
}

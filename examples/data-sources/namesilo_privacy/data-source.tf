data "namesilo_privacy" "example" {
  domain = "example.com"
}

output "privacy_enabled" {
  value = data.namesilo_privacy.example.enabled
}

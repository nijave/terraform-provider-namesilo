data "namesilo_domain" "example" {
  domain = "example.com"
}

output "expires" {
  value = data.namesilo_domain.example.expires
}

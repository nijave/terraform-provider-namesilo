data "namesilo_nameservers" "example" {
  domain = "example.com"
}

output "delegation" {
  value = data.namesilo_nameservers.example.nameservers
}

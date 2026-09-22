resource "namesilo_nameservers" "example" {
  domain = "example.com"

  nameservers = [
    "ns1.example.net",
    "ns2.example.net",
  ]
}

output "delegation" {
  value = namesilo_nameservers.example.nameservers
}

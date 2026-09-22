data "namesilo_domains" "account" {}

output "domain_names" {
  value = [for domain in data.namesilo_domains.account.domains : domain.name]
}

output "total" {
  value = data.namesilo_domains.account.total
}

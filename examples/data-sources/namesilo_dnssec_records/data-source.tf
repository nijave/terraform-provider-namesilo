data "namesilo_dnssec_records" "example" {
  domain = "example.com"
}

output "ds_records" {
  value = data.namesilo_dnssec_records.example.records
}

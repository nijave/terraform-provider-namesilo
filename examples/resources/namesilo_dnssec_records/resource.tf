resource "namesilo_dnssec_records" "example" {
  domain = "example.com"

  records = [{
    key_tag     = 2371
    algorithm   = 13
    digest_type = 2
    digest      = "8f9e1c6f5d5d5c9a1e0d3b7f2a4c6e8d0b2f4a6c8e0d2f4a6b8c0e2d4f6a8c0e"
  }]
}

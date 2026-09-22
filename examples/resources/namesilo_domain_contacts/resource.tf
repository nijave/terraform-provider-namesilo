resource "namesilo_contact" "example" {
  first_name = "Jane"
  last_name  = "Doe"
  address    = "123 Main Street"
  city       = "Springfield"
  state      = "IL"
  zip        = "62704"
  country    = "US"
  email      = "jane.doe@example.com"
  phone      = "+1.5555551234"
}

resource "namesilo_domain_contacts" "example" {
  domain     = "example.com"
  registrant = namesilo_contact.example.id
  billing    = namesilo_contact.example.id
}

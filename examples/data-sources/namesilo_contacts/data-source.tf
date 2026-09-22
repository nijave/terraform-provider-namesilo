data "namesilo_contacts" "all" {}

output "default_contact_id" {
  value = [for contact in data.namesilo_contacts.all.contacts : contact.contact_id if contact.default_profile][0]
}

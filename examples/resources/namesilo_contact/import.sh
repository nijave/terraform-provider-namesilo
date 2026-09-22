# The import ID is the API's contact_id, the opaque account-scoped identifier
# NameSilo assigns the profile. It is not part of the resource's configuration:
# read it from the account's contact list in the NameSilo UI, from the `id`
# attribute of a `namesilo_contact` resource in state, or from the `contacts`
# set the `namesilo_contacts` data source returns. Import seeds `id`, and the
# refresh that follows reads the profile from the API, so the first plan after
# import compares configuration against the existing profile instead of
# creating a second one.
tofu import namesilo_contact.example 1001

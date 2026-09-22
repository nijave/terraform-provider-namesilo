# The import ID is the domain name. Import seeds domain and id, then the
# refresh that follows reads the DS records the registry publishes. Importing
# an unsigned domain produces an empty records set.
tofu import namesilo_dnssec_records.example example.com

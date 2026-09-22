# The import ID is the domain name. Import seeds domain and id, then the
# refresh that follows reads the domain's current role assignments and stores
# every configured role; omitted roles are stored as computed and do not diff.
tofu import namesilo_domain_contacts.example example.com

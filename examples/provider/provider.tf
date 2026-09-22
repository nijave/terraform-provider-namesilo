terraform {
  required_providers {
    namesilo = {
      source  = "nijave/namesilo"
      version = "~> 0.1"
    }
  }
}

# The API key grants full account access. Pass it as a sensitive variable and
# set it through the environment rather than committing it to source control.
variable "namesilo_api_key" {
  type      = string
  sensitive = true
}

provider "namesilo" {
  api_key = var.namesilo_api_key

  # endpoint defaults to NameSilo's production API at
  # https://www.namesilo.com/api; it can also come from the
  # NAMESILO_API_ENDPOINT environment variable. Set it only to point at a
  # proxy or a test endpoint. A trailing slash is trimmed.
  # endpoint = "https://www.namesilo.com/api"
}

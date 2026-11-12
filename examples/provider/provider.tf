terraform {
  required_providers {
    omadafusion = {
      source = "inkstom/omadafusion"
    }
  }
}

# Credentials come from the environment:
#   export OMADA_URL=https://192.168.0.1
#   export OMADA_USERNAME=terraform
#   export OMADA_PASSWORD=...
provider "omadafusion" {
  site     = "Default"
  insecure = true # integrated controllers use a self-signed certificate
}

terraform {
  required_version = ">= 1.8.0"
  required_providers {
    sdwan = {
      source  = "CiscoDevNet/sdwan"
      version = "0.11.5"
    }
    utils = {
      source  = "netascode/utils"
      version = "2.0.3"
    }
    local = {
      source  = "hashicorp/local"
      version = "2.9.1"
    }
  }
}
# Credentials come only from SDWAN_* environment variables.
provider "sdwan" {
  retries = 0
}
module "sdwan" {
  source = "git::https://github.com/netascode/terraform-sdwan-nac-sdwan.git?ref=5fbf037ea57368c7e6e77694c81330b6f7b16666"
  yaml_files = ["${path.module}/../banner.yaml", "${path.module}/../system-profile.yaml"]
}

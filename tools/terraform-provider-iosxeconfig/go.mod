// Separate Go module so the Terraform-plugin-framework runtime
// stays out of the main controller's dependency graph. See
// README.md for the rationale.
module github.com/cisco-open/terraform-provider-iosxeconfig

go 1.25

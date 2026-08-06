resource "boomi_data_integration_blueprint" "example" {
  name          = "Posts API"
  file_cross_id = "your-blueprint-file-id"  # from boomi_data_integration_blueprint_file
}

# Upload the blueprint YAML content. The id returned here is what blueprint
# resources reference as file_cross_id.
resource "boomi_data_integration_blueprint_file" "posts_api" {
  filename = "posts_api.yaml"
  content  = file("${path.module}/posts_api.yaml")
}

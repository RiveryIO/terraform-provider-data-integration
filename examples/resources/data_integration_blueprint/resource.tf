resource "boomi_data_integration_blueprint_file" "posts_api" {
  filename = "posts_api.yaml"
  content  = file("${path.module}/posts_api.yaml")
}

resource "boomi_data_integration_blueprint" "posts_api" {
  name          = "Posts API"
  file_cross_id = boomi_data_integration_blueprint_file.posts_api.id
}

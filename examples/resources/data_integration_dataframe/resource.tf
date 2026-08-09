resource "boomi_data_integration_dataframe" "example" {
  environment_id = var.environment_id
  name           = "sales-staging"

  connection_settings = {
    connection     = boomi_data_integration_connection.s3_fz.id
    datasource_id  = "aws"
    storage_type   = "s3"
    default_bucket = "my-data-bucket"
  }
}
